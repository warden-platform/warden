ALTER TABLE access_requests
    ADD COLUMN execution_id UUID REFERENCES executions(id);

CREATE UNIQUE INDEX idx_access_requests_execution
    ON access_requests(execution_id)
    WHERE execution_id IS NOT NULL;
