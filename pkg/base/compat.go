package base

// CompatResult describes whether two base-service versions can interoperate.
type CompatResult int

const (
	Compatible      CompatResult = iota // same major, minor >= required
	UpgradeRequired                     // same major, minor < required
	Incompatible                        // different major
)

// VersionCompatibility reports whether a publisher at version publisherVer
// can interoperate with a consumer at version consumerVer. Both are the
// major version number from the base-service module (e.g. v1.2.3 → 1).
//
// Rules:
//   - Same major → compatible (additive contract: new minor versions add
//     optional fields; readers ignore unknowns).
//   - Publisher major > consumer major → incompatible (consumer cannot
//     understand new required fields).
//   - Publisher major < consumer major → upgrade_required (publisher is
//     behind; consumer may still accept with degraded features).
func VersionCompatibility(publisherVer, consumerVer int) CompatResult {
	if publisherVer == consumerVer {
		return Compatible
	}
	if publisherVer < consumerVer {
		return UpgradeRequired
	}
	return Incompatible
}