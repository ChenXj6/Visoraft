package tasks

import "testing"

func TestHardcodedChineseSubtitleResultRequiresDetectionEvidence(t *testing.T) {
	valid := SubtitleProcessingResult{
		Decision: SubtitleProcessingDecision{
			SchemaVersion:      1,
			Disposition:        "existing_hardcoded_chinese",
			TranslationSkipped: true,
			BurnSubtitles:      false,
			Detection: ExistingSubtitleDetectionResult{
				SchemaVersion:     1,
				State:             "found",
				Source:            "hardcoded",
				ConfidencePercent: 92,
				SampleCount:       32,
				StablePairCount:   13,
			},
		},
	}
	if err := normalizeAndValidateSubtitleResult(&valid); err != nil {
		t.Fatalf("expected valid hardcoded result, got %v", err)
	}

	invalid := valid
	invalid.Decision.Detection.StablePairCount = 0
	if err := normalizeAndValidateSubtitleResult(&invalid); err == nil {
		t.Fatal("expected missing stable-pair evidence to be rejected")
	}
}

func TestGeneratedSubtitleResultNeedsDocuments(t *testing.T) {
	result := SubtitleProcessingResult{
		Decision: SubtitleProcessingDecision{
			SchemaVersion: 1,
			Disposition:   "generated_subtitles",
		},
	}
	if err := normalizeAndValidateSubtitleResult(&result); err == nil {
		t.Fatal("expected generated result without documents to be rejected")
	}
}

func TestExistingSoftChineseResultRequiresReusableSourceEvidence(t *testing.T) {
	result := SubtitleProcessingResult{
		Documents: []SubtitleDocumentResult{{
			DocumentID: "document-id",
			Kind:       "original",
			Language:   "zh-Hans",
			Source:     "embedded",
			Segments:   []map[string]any{{"text": "中文字幕"}},
		}},
		Decision: SubtitleProcessingDecision{
			SchemaVersion:      1,
			Disposition:        "existing_soft_chinese",
			TranslationSkipped: true,
			BurnSubtitles:      false,
			Detection: ExistingSubtitleDetectionResult{
				SchemaVersion:     1,
				State:             "found",
				Source:            "embedded",
				ConfidencePercent: 100,
			},
		},
	}
	if err := normalizeAndValidateSubtitleResult(&result); err != nil {
		t.Fatalf("expected reusable embedded subtitles to pass: %v", err)
	}
	result.Decision.Detection.Source = "hardcoded"
	if err := normalizeAndValidateSubtitleResult(&result); err == nil {
		t.Fatal("expected invalid soft subtitle source to be rejected")
	}
}

func TestValidateSubtitleProcessingResultRejectsEmptyDocumentLanguage(t *testing.T) {
	result := SubtitleProcessingResult{
		Documents: []SubtitleDocumentResult{{
			DocumentID: "document-id",
			Kind:       "original",
			Language:   "",
			Source:     "asr",
			Segments:   []map[string]any{{"text": "字幕"}},
		}},
		Decision: SubtitleProcessingDecision{
			SchemaVersion: 1,
			Disposition:   "generated_subtitles",
		},
	}
	if err := ValidateSubtitleProcessingResult(&result); err == nil {
		t.Fatal("expected empty document language to be rejected before persistence")
	}
}
