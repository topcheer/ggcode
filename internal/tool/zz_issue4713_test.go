package tool

import (
	"strings"
	"testing"
)

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
