package models

import "time"

type Movement struct {
	ID            int
	MaterialID    int
	UserID        int
	Type          string
	Quantity      int
	Date          time.Time
	Material      string
	User          string
	FormattedDate string
	FormattedTime string
	FormattedType string
}
