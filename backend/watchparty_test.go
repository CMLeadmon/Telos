package main

import (
	"math"
	"testing"
)

func TestWatchPartyValidateControl(t *testing.T) {
	ok := []WatchPartyControlInput{
		{Action: "play", PositionSeconds: 10, PlaybackRate: 1},
		{Action: "play", PositionSeconds: 0, PlaybackRate: 0.5},
		{Action: "play", PositionSeconds: 0, PlaybackRate: 2},
		{Action: "pause", PositionSeconds: 5},
		{Action: "seek", PositionSeconds: 30},
		{Action: "change_media", PositionSeconds: 0, MediaItemID: "abc123"},
	}
	for i, in := range ok {
		if err := validateControl(in); err != nil {
			t.Errorf("case %d should be valid: %v", i, err)
		}
	}
	bad := []WatchPartyControlInput{
		{Action: "play", PositionSeconds: -1, PlaybackRate: 1},
		{Action: "play", PositionSeconds: math.Inf(1), PlaybackRate: 1},
		{Action: "play", PositionSeconds: 0, PlaybackRate: 0.4},
		{Action: "play", PositionSeconds: 0, PlaybackRate: 2.1},
		{Action: "change_media", PositionSeconds: 0, MediaItemID: "bad id!"},
		{Action: "explode", PositionSeconds: 0},
	}
	for i, in := range bad {
		if err := validateControl(in); err == nil {
			t.Errorf("bad case %d should be rejected: %+v", i, in)
		}
	}
}
