package migration

func (m *MigrationHeader) GetRsyncFeaturesSlice() []string {
	features := []string{}
	if m == nil {
		return features
	}

	if m.RsyncFeatures != nil {
		if m.RsyncFeatures.Xattrs != nil && *m.RsyncFeatures.Xattrs == true {
			features = append(features, "xattrs")
		}

		if m.RsyncFeatures.Delete != nil && *m.RsyncFeatures.Delete == true {
			features = append(features, "delete")
		}

		if m.RsyncFeatures.Compress != nil && *m.RsyncFeatures.Compress == true {
			features = append(features, "compress")
		}

		if m.RsyncFeatures.Bidirectional != nil && *m.RsyncFeatures.Bidirectional == true {
			features = append(features, "bidirectional")
		}
	}

	return features
}

func (m *MigrationHeader) GetZfsFeaturesSlice() []string {
	features := []string{}
	if m == nil {
		return features
	}

	if m.ZfsFeatures != nil {
		if m.ZfsFeatures.Compress != nil && *m.ZfsFeatures.Compress == true {
			features = append(features, "compress")
		}
	}

	return features
}
