package tasks

import "testing"

func TestValidateMediaInfoAcceptsNormalizedProbe(t *testing.T) {
	duration := 2.5
	size := int64(1024)
	width := 1920
	height := 1080
	info := MediaInfo{
		SchemaVersion:   1,
		FormatName:      "mov,mp4",
		DurationSeconds: &duration,
		SizeBytes:       &size,
		VideoCodec:      "h264",
		Width:           &width,
		Height:          &height,
		AudioCodec:      "aac",
		StreamCount:     2,
	}

	if err := validateMediaInfo(info); err != nil {
		t.Fatalf("validateMediaInfo() error = %v", err)
	}
}

func TestValidateMediaInfoRejectsUnsupportedSchema(t *testing.T) {
	info := MediaInfo{
		SchemaVersion: 2,
		FormatName:    "wav",
		StreamCount:   1,
	}

	if err := validateMediaInfo(info); err == nil {
		t.Fatal("validateMediaInfo() expected an error")
	}
}

func TestValidateMediaInfoRejectsStreamBomb(t *testing.T) {
	info := MediaInfo{
		SchemaVersion: 1,
		FormatName:    "matroska",
		StreamCount:   65,
	}

	if err := validateMediaInfo(info); err == nil {
		t.Fatal("validateMediaInfo() expected an error")
	}
}
