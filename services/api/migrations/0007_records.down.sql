-- Destructive: drops all course, batch, student and enrollment data. Take a backup first (brain §10.3).
DROP TABLE IF EXISTS enrollments;
DROP TABLE IF EXISTS students;
DROP TABLE IF EXISTS batches;
DROP TABLE IF EXISTS courses;
