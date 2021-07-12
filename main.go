package main

import (
	"encoding/json"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dxe/alc-mobile-api/model"

	"github.com/coreos/go-oidc"
	"github.com/jmoiron/sqlx"
	"golang.org/x/oauth2"

	_ "github.com/go-sql-driver/mysql"
)

var (
	flagProd = flag.Bool("prod", false, "whether to run in production mode")
)

func config(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	log.Fatalf("missing configuration for %v", key)
	panic("unreachable")
}

func main() {
	flag.Parse()

	// TODO(mdempsky): Generalize.
	r := http.DefaultServeMux

	connectionString := config("DB_USER") + ":" + config("DB_PASSWORD") +
		"@" + config("DB_PROTOCOL") + "/" + config("DB_NAME") +
		"?parseTime=true&charset=utf8mb4"
	if *flagProd {
		connectionString += "&tls=true"
	}
	db := model.NewDB(connectionString)

	// TODO: Consider not doing this each time the application loads.
	// It may be better to do it via a script instead.
	if !*flagProd {
		model.WipeDatabase(db)
		model.InitDatabase(db)
		model.InsertMockData(db)
	}

	clientID := config("OAUTH_CLIENT_ID")
	clientSecret := config("OAUTH_CLIENT_SECRET")

	conf, verifier, err := newGoogleVerifier(clientID, clientSecret)
	if err != nil {
		log.Fatalf("failed to create Google OIDC verifier: %v", err)
	}

	newServer := func(w http.ResponseWriter, r *http.Request) *server {
		return &server{
			conf:     conf,
			verifier: verifier,

			db: db,
			w:  w,
			r:  r,
		}
	}

	handle := func(path string, method func(*server)) {
		r.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			method(newServer(w, r))
		})
	}

	// handleAuth is like handle, but it requires the user to be logged
	// in with OAuth2 credentials first. Currently, this means with an
	// @directactioneverywhere.com account, because our OAuth2 settings
	// are configured to "Internal".
	handleAuth := func(path string, method func(*server)) {
		r.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			s := newServer(w, r)

			email, err := s.googleEmail()
			if err != nil {
				s.redirect(absURL("/login"))
				return
			}
			s.email = email

			method(s)
		})
	}

	// Admin pages
	handleAuth("/", (*server).index)
	handle("/login", (*server).login)
	handle("/logout", (*server).logout)
	handle("/auth", (*server).auth)
	handleAuth("/admin", (*server).admin)
	handleAuth("/admin/conferences", (*server).adminConferences)
	handleAuth("/admin/conference/details", (*server).adminConferenceDetails)
	handleAuth("/admin/conference/save", (*server).adminConferenceSave)
	handleAuth("/admin/conference/delete", (*server).adminConferenceDelete)
	handleAuth("/admin/locations", (*server).adminLocations)
	handleAuth("/admin/locations/edit", (*server).adminLocations)
	handleAuth("/admin/locations/update", (*server).adminLocations)
	handleAuth("/admin/locations/delete", (*server).adminLocations)
	handleAuth("/admin/events", (*server).adminEvents)
	handleAuth("/admin/events/edit", (*server).adminEvents)
	handleAuth("/admin/events/update", (*server).adminEvents)
	handleAuth("/admin/events/delete", (*server).adminEvents)
	handleAuth("/admin/info", (*server).adminInfo)
	handleAuth("/admin/info/edit", (*server).adminInfo)
	handleAuth("/admin/info/update", (*server).adminInfo)
	handleAuth("/admin/info/delete", (*server).adminInfo)
	handleAuth("/admin/announcements", (*server).adminAnnouncements)
	handleAuth("/admin/announcements/edit", (*server).adminAnnouncements)
	handleAuth("/admin/announcements/update", (*server).adminAnnouncements)
	handleAuth("/admin/announcements/delete", (*server).adminAnnouncements)

	// Healthcheck page for load balancer
	handle("/healthcheck", (*server).health)

	// Unauthed API
	handle("/conference/list", (*server).listConferences)
	handle("/info/list", (*server).listInfo)
	handle("/announcement/list", (*server).listAnnouncements)
	handle("/event/list", (*server).listEvents)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	log.Println("Server started. Listening on port 8080.")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

type server struct {
	conf     *oauth2.Config
	verifier *oidc.IDTokenVerifier

	email string

	db *sqlx.DB
	w  http.ResponseWriter
	r  *http.Request
}

func (s *server) index() {
	s.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.r.URL.Path != "/" {
		http.NotFound(s.w, s.r)
		return
	}
	s.redirect(absURL("/admin"))
}

func (s *server) renderTemplate(name string, pageData interface{}) {
	type templateData struct {
		UserEmail string
		PageName  string
		PageData  interface{}
	}
	data := templateData{
		UserEmail: s.email,
		PageName:  name,
		PageData:  pageData,
	}

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"emailToName": func(email string) string {
			components := strings.Split(email, "@")
			return strings.Title(components[0])
		},
	}).ParseGlob("templates/*.html")
	if err != nil {
		log.Println(err)
		panic("failed to parse template")
	}
	if err := tmpl.ExecuteTemplate(s.w, name+".html", data); err != nil {
		log.Println(err)
		panic("failed to execute template")
	}
}

func (s *server) admin() {
	s.renderTemplate("index", nil)
}

func (s *server) adminConferences() {
	conferenceData, err := model.ListConferences(s.db)
	// TODO(jhobbs): Consider not returning an error if no conferences are found to make this more simple.
	if err != nil && err.Error() != "no conferences found" {
		panic(err)
	}
	s.renderTemplate("conferences", conferenceData)
}

func (s *server) adminConferenceDetails() {
	id := s.r.URL.Query().Get("id")
	if id == "" {
		// Form to create a new conference
		s.renderTemplate("conference_details", model.Conference{})
		return
	}
	// Form to update an existing conference
	conference, err := model.GetConferenceByID(s.db, id)
	if err != nil {
		s.adminError(err)
		return
	}
	log.Println(conference)
	s.renderTemplate("conference_details", conference)
}

func (s *server) adminConferenceSave() {
	if err := s.r.ParseForm(); err != nil {
		s.adminError(err)
		return
	}

	id, err := strconv.Atoi(s.r.Form.Get("ID"))
	if err != nil {
		s.adminError(err)
		return
	}
	// TODO: validate that start & end time format before attempting to save
	// and provide a more clear error message for users?

	conference := model.Conference{
		ID:        id,
		Name:      s.r.Form.Get("Name"),
		StartDate: s.r.Form.Get("StartTime"),
		EndDate:   s.r.Form.Get("EndTime"),
	}
	// update the database
	if err := model.SaveConference(s.db, conference); err != nil {
		s.adminError(err)
		return
	}
	s.redirect("/admin/conferences")
}

func (s *server) adminConferenceDelete() {
	id := s.r.URL.Query().Get("id")
	if err := model.DeleteConference(s.db, id); err != nil {
		s.adminError(err)
		return
	}
	s.redirect("/admin/conferences")
}

func (s *server) adminLocations() {
	locationData, err := model.ListLocations(s.db)
	// TODO(jhobbs): Consider not returning an error if no locations are found to make this more simple.
	if err != nil && err.Error() != "no locations found" {
		panic(err)
	}
	s.renderTemplate("locations", locationData)
}

func (s *server) adminEvents() {
	eventData, err := model.ListEvents(s.db, model.EventOptions{ConferenceID: 1})
	// TODO(jhobbs): Consider not returning an error if no events are found to make this more simple.
	if err != nil && err.Error() != "no events found" {
		panic(err)
	}
	s.renderTemplate("events", eventData)
}

func (s *server) adminInfo() {
	infoData, err := model.ListInfo(s.db)
	// TODO(jhobbs): Consider not returning an error if no info is found to make this more simple.
	if err != nil && err.Error() != "no info found" {
		panic(err)
	}
	s.renderTemplate("info", infoData)
}

func (s *server) adminAnnouncements() {
	announcementData, err := model.ListAnnouncements(s.db, model.AnnouncementOptions{ConferenceID: 1, IncludeScheduled: true})
	// TODO(jhobbs): Consider not returning an error if no announcements are found to make this more simple.
	if err != nil && err.Error() != "no announcements found" {
		panic(err)
	}
	s.renderTemplate("announcements", announcementData)
}

func (s *server) adminError(err error) {
	s.renderTemplate("error", err.Error())
}

func (s *server) listConferences() {
	s.serveJSON(model.ListConferences(s.db))
}

func (s *server) listInfo() {
	s.serveJSON(model.ListInfo(s.db))
}

func (s *server) listAnnouncements() {
	// TODO: pass in the conference id as a parameter
	s.serveJSON(model.ListAnnouncements(s.db, model.AnnouncementOptions{
		IncludeScheduled: false,
		ConferenceID:     1,
	}))
}

func (s *server) listEvents() {
	// TODO: pass in the conference id as a parameter
	s.serveJSON(model.ListEvents(s.db, model.EventOptions{
		ConferenceID: 1,
	}))
}

func (s *server) health() {
	s.serveJSON("OK", nil)
}

func (s *server) serveJSON(data interface{}, err error) {
	if err != nil {
		s.writeJSON(map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}
	s.w.WriteHeader(http.StatusOK)
	s.writeJSON(map[string]interface{}{
		"status": "success",
		"data":   data,
	})
}

func (s *server) writeJSON(v interface{}) {
	s.w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(s.w)
	err := enc.Encode(v)
	if err != nil {
		log.Printf("Error writing JSON: %v", err.Error())
	}
}

func (s *server) redirect(dest string) {
	http.Redirect(s.w, s.r, dest, http.StatusFound)
}

func absURL(path string) string {
	// TODO(mdempsky): Use URL relative path resolution here?
	return config("BASE_URL") + path
}
