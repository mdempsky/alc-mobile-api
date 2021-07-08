package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

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

	connectionString := os.Getenv("DB_USER") + ":" + os.Getenv("DB_PASSWORD") + "@" + os.Getenv("DB_PROTOCOL") + "/" + os.Getenv("DB_NAME") + "?parseTime=true&charset=utf8mb4"
	if *flagProd {
		connectionString += "&tls=true"
	}
	db := model.NewDB(connectionString)

	clientID := os.Getenv("OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("OAUTH_CLIENT_SECRET")

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

	handleAuth("/", (*server).index)
	handle("/login", (*server).login)
	handle("/auth", (*server).auth)
	handle("/healthcheck", (*server).health)

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
	fmt.Fprintf(s.w, "Hello, %s\n", s.email)
	if isAdmin(s.email) {
		fmt.Fprintf(s.w, "(Psst, you're an admin!)\n")
	}
}

func (s *server) health() {
	s.w.WriteHeader(http.StatusOK)
	s.w.Write([]byte("OK"))
}

func (s *server) error(err error) {
	s.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(s.w, err)
}

func (s *server) redirect(dest string) {
	http.Redirect(s.w, s.r, dest, http.StatusFound)
}

func absURL(path string) string {
	// TODO(mdempsky): Use URL relative path resolution here? Or add a
	// flag to control the base URL?
	base := "http://localhost:8080"
	if *flagProd {
		base = "https://alc-mobile-api.dxe.io"
	}
	return base + path
}
