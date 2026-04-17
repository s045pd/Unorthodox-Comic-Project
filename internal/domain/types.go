package domain

import "time"

type Book struct {
	ID          string
	Title       string
	Description string
	Hot         int
	RawURL      string
	ImageURL    string
	CoverPath   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Episode struct {
	ID        int64
	BookID    string
	Title     string
	RawURL    string
	PDFPath   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Image struct {
	ID        int64
	EpisodeID int64
	Index     int
	RawURL    string
	FilePath  string
	Width     int
	Height    int
	Bytes     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Tag struct {
	ID   int64
	Name string
}
