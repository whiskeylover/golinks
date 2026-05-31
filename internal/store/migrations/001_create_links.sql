CREATE TABLE links (
	shortcut TEXT PRIMARY KEY,
	destination_url TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
