-- Destructive: drops all delivery history.
DROP TABLE IF EXISTS delivery_queue;
DROP TABLE IF EXISTS delivery_attempts;
DROP TABLE IF EXISTS deliveries;
DROP FUNCTION IF EXISTS deliveries_guard();
