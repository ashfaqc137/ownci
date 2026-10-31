//go:build !dev

package github

// Release builds do not compile in the dev overrides.
func devAPIURL() string { return "" }
func devWebURL() string { return "" }
