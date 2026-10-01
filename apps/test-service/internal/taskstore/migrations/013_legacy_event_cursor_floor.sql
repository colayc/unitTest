-- Preserve the historical AUTOINCREMENT high-water even if later retention
-- removes the final legacy cursor tombstone or ordinary global event.
CREATE TABLE legacy_event_cursor_floor (
  singleton INTEGER PRIMARY KEY CHECK (singleton=1),
  sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 9007199254740991)
);
INSERT INTO legacy_event_cursor_floor(singleton,sequence)
VALUES(1,COALESCE((SELECT seq FROM sqlite_sequence WHERE name='task_events'),0));
