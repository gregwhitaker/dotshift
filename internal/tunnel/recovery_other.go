//go:build !darwin

package tunnel

func rememberFunnel(string, int) error { return nil }
func forgetFunnel(string, int)         {}
func recoverFunnel(string) error       { return nil }
