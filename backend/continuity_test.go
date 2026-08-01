package main

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestValidateMemberProgressByKind(t *testing.T) {
	tests := []struct {
		name        string
		kind        CatalogKind
		in          ProgressInput
		ok          bool
		wantLocator string
	}{
		{
			name: "epub locator",
			kind: "epub",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"epubcfi(/6/4!/4)","fraction":0.75}`),
				Percent: 0.75,
			},
			ok: true,
		},
		{
			name: "pdf locator",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":12,"zoom":1.25}`),
				Percent: 0.5,
			},
			ok: true,
		},
		{
			name: "pdf exact upper bounds",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":100000,"zoom":10}`),
				Percent: 1,
			},
			ok: true,
		},
		{
			name: "audiobook locator",
			kind: "audiobook",
			in: ProgressInput{
				Locator:    json.RawMessage(`{"trackIndex":2}`),
				PositionMS: 9000,
				DurationMS: 12000,
				Percent:    0.75,
			},
			ok: true,
		},
		{
			name: "video locator",
			kind: "video",
			in: ProgressInput{
				Locator:    json.RawMessage(`{}`),
				PositionMS: 3000,
				DurationMS: 12000,
				Percent:    0.25,
			},
			ok: true,
		},
		{
			name: "audio locator",
			kind: "audio",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
			},
			ok: true,
		},
		{
			name:        "omitted video locator defaults to empty object",
			kind:        "video",
			in:          ProgressInput{},
			ok:          true,
			wantLocator: `{}`,
		},
		{
			name: "four kibibyte locator",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{}` + strings.Repeat(" ", 4094)),
			},
			ok: true,
		},
		{
			name: "locator over four kibibytes",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{}` + strings.Repeat(" ", 4095)),
			},
		},
		{
			name: "negative position",
			kind: "video",
			in: ProgressInput{
				Locator:    json.RawMessage(`{}`),
				PositionMS: -1,
			},
		},
		{
			name: "negative duration",
			kind: "video",
			in: ProgressInput{
				Locator:    json.RawMessage(`{}`),
				DurationMS: -1,
			},
		},
		{
			name: "position after nonzero duration",
			kind: "video",
			in: ProgressInput{
				Locator:    json.RawMessage(`{}`),
				PositionMS: 12001,
				DurationMS: 12000,
			},
		},
		{
			name: "position allowed with unknown duration",
			kind: "video",
			in: ProgressInput{
				Locator:    json.RawMessage(`{}`),
				PositionMS: 12001,
			},
			ok: true,
		},
		{
			name: "negative percent",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
				Percent: -0.01,
			},
		},
		{
			name: "percent over one",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
				Percent: 1.01,
			},
		},
		{
			name: "nonfinite percent",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
				Percent: math.NaN(),
			},
		},
		{
			name: "negative audiobook track",
			kind: "audiobook",
			in: ProgressInput{
				Locator: json.RawMessage(`{"trackIndex":-1}`),
			},
		},
		{
			name: "missing audiobook track",
			kind: "audiobook",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
			},
		},
		{
			name: "epub empty cfi",
			kind: "epub",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"","fraction":0.5}`),
			},
		},
		{
			name: "epub fraction out of range",
			kind: "epub",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"x","fraction":1.1}`),
			},
		},
		{
			name: "epub fraction just above one does not round into range",
			kind: "epub",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"x","fraction":1.000000000000000001}`),
			},
		},
		{
			name: "pdf page just above maximum does not round into range",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":100000.000000000001,"zoom":1}`),
			},
		},
		{
			name: "large fractional audiobook track does not round to integer",
			kind: "audiobook",
			in: ProgressInput{
				Locator: json.RawMessage(`{"trackIndex":9007199254740992.5}`),
			},
		},
		{
			name: "pdf page is not an integer",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":1.5,"zoom":1}`),
			},
		},
		{
			name: "pdf zoom out of range",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":1,"zoom":10.1}`),
			},
		},
		{
			name: "pdf zoom just above maximum does not round into range",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":1,"zoom":10.000000000000000001}`),
			},
		},
		{
			name: "fractional audiobook track",
			kind: "audiobook",
			in: ProgressInput{
				Locator: json.RawMessage(`{"trackIndex":1.5}`),
			},
		},
		{
			name: "epub with pdf field",
			kind: "epub",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"x","fraction":0.5,"page":2}`),
			},
		},
		{
			name: "pdf with epub field",
			kind: "pdf",
			in: ProgressInput{
				Locator: json.RawMessage(`{"page":2,"zoom":1,"cfi":"x"}`),
			},
		},
		{
			name: "audiobook with pdf field",
			kind: "audiobook",
			in: ProgressInput{
				Locator: json.RawMessage(`{"trackIndex":2,"page":1}`),
			},
		},
		{
			name: "video with epub field",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{"cfi":"x"}`),
			},
		},
		{
			name: "folder has no progress locator",
			kind: "folder",
			in: ProgressInput{
				Locator: json.RawMessage(`{}`),
			},
		},
		{
			name: "locator must be an object",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`[]`),
			},
		},
		{
			name: "locator must be valid json",
			kind: "video",
			in: ProgressInput{
				Locator: json.RawMessage(`{`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateProgress(tt.kind, tt.in)
			if tt.ok {
				if err != nil {
					t.Fatalf("ValidateProgress: %v", err)
				}
				if got.PositionMS != tt.in.PositionMS {
					t.Fatalf("positionMs = %d, want %d", got.PositionMS, tt.in.PositionMS)
				}
				if tt.wantLocator != "" && string(got.Locator) != tt.wantLocator {
					t.Fatalf("locator = %s, want %s", got.Locator, tt.wantLocator)
				}
				return
			}
			if !errors.Is(err, errProgressInvalid) {
				t.Fatalf("error = %v, want errProgressInvalid", err)
			}
		})
	}
}
