package userdata

// Prepare runs an optional apply hook then an optional validate hook.
// Both hooks are safe to be nil. Returns the validation error, if any.
// Callers use this to centralize the applyDefaults → validate pre-render
// chain shared across Generate/GenerateRaw wrappers.
func Prepare(apply func(), validate func() error) error {
	if apply != nil {
		apply()
	}
	if validate != nil {
		return validate()
	}
	return nil
}
