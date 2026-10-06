package main

import (
	"regexp"
	"testing"
	"time"
)

// Test helper for checking if a string slice contains a specific string
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func testMatchRegex(t *testing.T, name string, re *regexp.Regexp, mustmatch, mustnotmatch []string) {
	for _, str := range mustmatch {
		if re.MatchString(str) == false {
			t.Errorf("regexp %s did not match test string: %s", name, str)
		}
	}
	for _, str := range mustnotmatch {
		if re.MatchString(str) == true {
			t.Errorf("regexp %s did match negative test string: %s", name, str)
		}
	}
}

func testSubmatchRegex(t *testing.T, name string, re *regexp.Regexp, teststr string, expectedresult []string) {
	matchlist := re.FindStringSubmatch(teststr)
	if len(expectedresult) != len(matchlist) {
		t.Errorf("regexp %s did not yield expected results in test: %s but %s", name, teststr, matchlist)
		return
	}
	for c, _ := range expectedresult {
		if expectedresult[c] != matchlist[c] {
			t.Errorf("regexp %s did match expected result in test %s as %s != %s", name, teststr, expectedresult[c], matchlist[c])
		}
	}
}

func TestRegexURLMatch(t *testing.T) {
	mastodon_urls := []string{"https://chaos.social/@qbit/102133941111331502",
		"https://mastodon.social/@test/102133941111331502",
		"https://chaos.social/web/statuses/102140251110038222",
		"http://chaos.social/web/statuses/102140251110038222",
		"https://chaos.social/web/statuses/1"}

	twitter_urls := []string{"https://twitter.com/someone/statuses/1131013299817111553",
		"https://mobile.twitter.com/realraum/status/1131013299817111553",
		"http://mobile.twitter.com/realraum/status/1131013299817111553",
		"https://twitter.com/someone/status/1131013299817111553",
		"http://twitter.com/someone/status/1131013299817111553",
	}

	testMatchRegex(t, "mastodon_status_uri_re_", mastodon_status_uri_re_, mastodon_urls, twitter_urls)
	testMatchRegex(t, "twitter_status_uri_re_", twitter_status_uri_re_, twitter_urls, mastodon_urls)
}

func TestRegexSubmatch(t *testing.T) {
	testSubmatchRegex(t, "mastodon_status_uri_re_", mastodon_status_uri_re_, "https://chaos.social/web/statuses/102140251110038222", []string{"https://chaos.social/web/statuses/102140251110038222", "102140251110038222"})
	testSubmatchRegex(t, "twitter_status_uri_re_", twitter_status_uri_re_, "https://twitter.com/someone/status/1131013299817111553", []string{"https://twitter.com/someone/status/1131013299817111553", "1131013299817111553"})
	testSubmatchRegex(t, "directmsg_re_", directmsg_re_, "blabla @user lala", []string{" @user ", "@user"})
	testSubmatchRegex(t, "directmsg_re_", directmsg_re_, "blabla @user@mastodon.social", []string{" @user@mastodon.social", "@user@mastodon.social"})
	testSubmatchRegex(t, "directmsg_re_", directmsg_re_, "@user@mastodon.social bla bla", []string{"@user@mastodon.social ", "@user@mastodon.social"})
	testSubmatchRegex(t, "directmsg_re_", directmsg_re_, "@user@mastodon.social", []string{"@user@mastodon.social", "@user@mastodon.social"})
}

func TestRegexDM(t *testing.T) {
	testMatchRegex(t, "directmsg_re_", directmsg_re_, []string{"@someone", "@someone@somewhere.social"}, []string{"email@example.com"})
}

// Test character limit checking
func TestCheckCharacterLimit(t *testing.T) {
	// Test short status
	shortStatus := "Hello world"
	if err := checkCharacterLimit(shortStatus); err != nil {
		t.Errorf("Short status should not exceed limit: %v", err)
	}

	// Test status with URL (URLs count as ~23 characters)
	statusWithURL := "Hello https://example.com world"
	if err := checkCharacterLimit(statusWithURL); err != nil {
		t.Errorf("Status with URL should not exceed limit: %v", err)
	}

	// Test very long status
	longStatus := ""
	for i := 0; i < 1000; i++ {
		longStatus += "a"
	}
	if err := checkCharacterLimit(longStatus); err == nil {
		t.Errorf("Very long status should exceed limit")
	}
}

// Test RemoveQuoteTextFromMatrixElementReplyMsg
func TestRemoveQuoteTextFromMatrixElementReplyMsg(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Single line quote",
			input:    "> Hello world",
			expected: "",
		},
		{
			name:     "Multi-line with quote then text",
			input:    "> Hello\n> world\nThis is my reply",
			expected: "This is my reply",
		},
		{
			name:     "No quote",
			input:    "This is my reply",
			expected: "This is my reply",
		},
		{
			name:     "Empty lines after quote",
			input:    "> Hello\n\n\nThis is my reply",
			expected: "This is my reply",
		},
		{
			name:     "Multiple empty lines",
			input:    "> Hello\n\n> world\n\n\nThis is my reply",
			expected: "This is my reply",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveQuoteTextFromMatrixElementReplyMsg(tt.input)
			if result != tt.expected {
				t.Errorf("RemoveQuoteTextFromMatrixElementReplyMsg(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test configuration sanity checks
func TestConfigSanityChecksAndDefaults(t *testing.T) {
	// This test would require setting up a config map
	// For now, we'll test that the function doesn't panic with valid config
	// In a real scenario, you'd want to test various edge cases
	// This is a placeholder to show where config validation tests would go
	t.Skip("Config validation tests require setup - skipping for now")
}

// Test time parsing for image timeout
func TestTimeParsing(t *testing.T) {
	// Test that we can parse various time durations
	durations := []string{
		"60m",
		"1h",
		"24h",
		"30s",
	}

	for _, dur := range durations {
		if _, err := time.ParseDuration(dur); err != nil {
			t.Errorf("Failed to parse duration %s: %v", dur, err)
		}
	}
}
