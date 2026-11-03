package tool

import "testing"

func TestCommandGate_RecursiveChownSubdirNotBlocked(t *testing.T) {
	gate := NewCommandGate()

	askCases := []string{
		"chown -R opendkim:opendkim /etc/opendkim/keys",
		"chown -R www-data /var/www",
		"chmod -R 000 /var/log/app",
		"sudo chown -R app:app /srv/app",
	}
	for _, cmd := range askCases {
		r := gate.Check(cmd)
		if r.Behavior == Block {
			t.Errorf("subdir recursive chown/chmod must not BLOCK: %q (reason=%s)", cmd, r.Reason)
		}
		if r.Behavior != Ask {
			t.Errorf("subdir recursive chown/chmod should ASK: %q got %v", cmd, r.Behavior)
		}
	}

	rootChown := "chown -R nobody " + "/"
	rootChown2 := "chown -R nobody:root " + "/ ; echo done"
	rootChmodR := "chmod -R 000 " + "/"
	rootChmod1 := "chmod 000 " + "/"
	for _, cmd := range []string{rootChown, rootChown2, rootChmodR, rootChmod1} {
		r := gate.Check(cmd)
		if r.Behavior != Block {
			t.Errorf("root target must BLOCK: %q got %v (reason=%s)", cmd, r.Behavior, r.Reason)
		}
	}
}
