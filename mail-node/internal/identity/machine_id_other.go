//go:build !unix

package identity

func readMachineID() ([]byte, error) { return nil, ErrMachineIDUnavailable }
