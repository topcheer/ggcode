package config

// DefaultSmallModel returns the vendor's generated default small-model id
// (models.dev default_small_model_id, see scripts/sync-model-caps.go).
// Most vendors currently ship no small-model default (""); in that case
// auxiliary routing stays off unless the user sets aux_model explicitly.
func DefaultSmallModel(vendor string) string {
	if info, ok := vendorDefaultModels[vendor]; ok {
		return info.SmallModel
	}
	return ""
}
