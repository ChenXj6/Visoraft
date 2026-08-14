ALTER TABLE review_actions
    DROP CONSTRAINT IF EXISTS review_actions_action_check;

ALTER TABLE review_actions
    ADD CONSTRAINT review_actions_action_check
    CHECK (
        action IN (
            'approve',
            'request_changes',
            'resubmit',
            'abandon',
            'subtitle_edit',
            'automatic_approve',
            'automatic_reject',
            'automatic_fallback'
        )
    );
