package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type Conference struct {
	ID        int    `db:"id" json:"id"`
	Name      string `db:"name" json:"name"`
	StartDate string `db:"start_date" json:"start_date"`
	EndDate   string `db:"end_date" json:"end_date"`
}

func ListConferences(db *sqlx.DB) ([]Conference, error) {
	const query = `
SELECT id, name,
   DATE_FORMAT(start_date,"%Y-%m-%d") as start_date,
   DATE_FORMAT(end_date,"%Y-%m-%d") as end_date
FROM conferences
`
	var conferences []Conference
	if err := db.Select(&conferences, query); err != nil {
		return conferences, fmt.Errorf("failed to list conferences: %w", err)
	}
	if conferences == nil {
		return nil, errors.New("no conferences found")
	}
	return conferences, nil
}

func DeleteConference(db *sqlx.DB, id string) error {
	if id == "" {
		return errors.New("conference id must be provided")
	}
	const query = "DELETE FROM conferences WHERE id = ?"
	res, err := db.Exec(query, id)
	if err != nil {
		if strings.Contains(err.Error(), "a foreign key constraint fails") {
			return fmt.Errorf("cannot delete conference without first deleting all conference data")
		}
		return fmt.Errorf("failed to delete conference: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("failed to delete conference: no rows affected")
	}
	return nil
}

func GetConferenceByID(db *sqlx.DB, id string) (Conference, error) {
	const query = `
SELECT id, name, 
	DATE_FORMAT(start_date,"%Y-%m-%d") as start_date,
	DATE_FORMAT(end_date,"%Y-%m-%d") as end_date
FROM conferences
WHERE id = ?
`
	var conferences []Conference
	if err := db.Select(&conferences, query, id); err != nil {
		return Conference{}, fmt.Errorf("failed to select conference: %w", err)
	}
	if len(conferences) == 0 {
		return Conference{}, errors.New("found no conference with given id")
	}
	return conferences[0], nil
}

func SaveConference(db *sqlx.DB, conference Conference) error {
	if conference.ID == 0 {
		return insertConference(db, conference)
	}
	return updateConference(db, conference)
}

func insertConference(db *sqlx.DB, conference Conference) error {
	query := "INSERT INTO conferences (name, start_date, end_date) VALUES (:name, :start_date, :end_date)"
	if _, err := db.NamedExec(query, conference); err != nil {
		return fmt.Errorf("failed to insert conference: %w", err)
	}
	return nil
}

func updateConference(db *sqlx.DB, conference Conference) error {
	query := "UPDATE conferences SET name = :name, start_date = :start_date, end_date = :end_date WHERE id = :id"
	if _, err := db.NamedExec(query, conference); err != nil {
		return fmt.Errorf("failed to update conference: %w", err)
	}
	return nil
}
