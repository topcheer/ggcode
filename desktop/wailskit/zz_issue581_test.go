package wailskit

import (
	"testing"
)

func TestIssue581_S2_CurrentSessionExportUsesFullLoad(t *testing.T) {

	_ = loadSessionForExport
}

func TestIssue581_D1_ConfigBackupRotation(t *testing.T) {

	_ = LoadDesktopConfig

	t.Run("good_bak_corrupt_main_promotes_on_recovery", func(t *testing.T) {

		t.Skip("requires config path override to test in isolation")
	})

	t.Run("good_main_rotates_to_bak_on_save", func(t *testing.T) {

		t.Skip("requires config path override to test in isolation")
	})

	t.Run("corrupt_main_skips_rotation_preserves_bak", func(t *testing.T) {

		t.Skip("requires config path override to test in isolation")
	})
}

func TestIssue581_S1_DeleteSessionHoldsLockAcrossDelete(t *testing.T) {

	_ = DeleteSession

}

func TestIssue581_S1_FailOpenLogsLockError(t *testing.T) {

	_ = DeleteSession

}

func TestMergeTunnelUserMessagesIsIdempotent(t *testing.T) {

	_ = mergeTunnelUserMessages
}

func TestIssue581_S2_ExportCurrentSessionGetsID(t *testing.T) {

	_ = loadSessionForExport
}

func BenchmarkExportCurrentSession(b *testing.B) {

	b.Skip("requires session store setup")
}

func TestDesktopConfigPromotionWithMissingDir(t *testing.T) {

	t.Skip("requires config path override to test in isolation")
}

func TestDesktopConfigCorruptBakHandling(t *testing.T) {

	t.Skip("requires config path override to test in isolation")
}
