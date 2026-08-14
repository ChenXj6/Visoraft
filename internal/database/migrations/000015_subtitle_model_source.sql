ALTER TABLE subtitle_documents
    DROP CONSTRAINT subtitle_documents_source_check;

ALTER TABLE subtitle_documents
    ADD CONSTRAINT subtitle_documents_source_check
    CHECK (
        source IN (
            'youtube_manual',
            'youtube_auto',
            'asr',
            'fixture',
            'model',
            'edited'
        )
    );

UPDATE subtitle_documents AS document
SET source = 'model'
WHERE document.kind = 'translated'
  AND document.source = 'edited'
  AND NOT EXISTS (
      SELECT 1
      FROM review_actions AS action
      WHERE action.action = 'subtitle_edit'
        AND action.payload ->> 'document_id' = document.id::text
  );
