-- Destructive: drops all import jobs (and any retained error-report rows). Take a backup first (brain §10.3).
DROP TABLE IF EXISTS student_imports;
