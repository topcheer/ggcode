package tool

import (
	"strings"
	"testing"
)

// Regression guard for #4713 (headless half): the screenshot headless mode is
// fully implemented on both backends (#851: skip imageResult, return a text
// summary) and the params struct parses it - but the JSON schema never
// declared it, so no model would ever pass it and the token-saving path was
// dead. The schema must expose headless as a boolean.
func TestIssue4713_HeadlessInSchema(t *testing.T) {
	schema := string((&MobileDeviceTool{}).Parameters())
	for _, frag := range []string{
		`"headless"`,
		`"type": "boolean"`,
		"skip returning the image data",
	} {
		if !strings.Contains(schema, frag) {
			t.Errorf("mobile_device schema lost required fragment %q (headless must stay declared)", frag)
		}
	}
}
