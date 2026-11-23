package core

import "testing"

func TestEnhancedAcceptable_SpokenCommands(t *testing.T) {
	raw := "Hi team new paragraph the meeting is at five new paragraph thanks"
	if !EnhancedAcceptable(raw, "Hi team,\n\nThe meeting is at 5.\n\nThanks.") {
		t.Error("a correct cleanup that consumed two commands was rejected by the deletion guard")
	}
	if EnhancedAcceptable("Hi team the meeting is at five in room four thanks everyone", "Hi team, the meeting is at 5.") {
		t.Error("a genuine deletion with no commands must still be rejected")
	}
}

func TestEnhancedAcceptable_SpokenNumbers(t *testing.T) {
	raw := "Harvey Bear New Paragraph seventy six twenty Blandford Place New Paragraph three oh three five zero"
	if !EnhancedAcceptable(raw, "Harvey Bear\n\n7620 Blandford Place\n\n30350") {
		t.Error("a correct cleanup that turned number words into digits was rejected by the deletion guard")
	}
	if EnhancedAcceptable("I need seven apples and three pears and some bread for the party tonight", "I need 7 apples.") {
		t.Error("a genuine deletion next to a number must still be rejected")
	}
}
