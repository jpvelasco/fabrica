package perforce

// DefaultHelixVersion is the p4d release Fabrica pins when config omits
// `version`. Perforce's apt archive drops old releases periodically; the
// userdata falls back to the current repo version when a pin disappears.
const DefaultHelixVersion = "2025.2"
