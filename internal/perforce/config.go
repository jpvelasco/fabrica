package perforce

// DefaultHelixVersion is the p4d release Fabrica pins when config omits
// `version`. Cloud-init installs the newest repo build of the pinned release
// and fails closed when Perforce's apt archive no longer carries it.
const DefaultHelixVersion = "2025.2"
