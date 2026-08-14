ALTER TABLE review_runs
    DROP CONSTRAINT IF EXISTS review_runs_decision_check;

ALTER TABLE review_runs
    ADD CONSTRAINT review_runs_decision_check
    CHECK (
        decision IN (
            '',
            'approved',
            'rejected',
            'manual_required',
            'changes_requested'
        )
    );
