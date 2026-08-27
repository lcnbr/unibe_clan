package main

import "testing"

func TestValidateListenAddress(t *testing.T) {
	valid := map[string]string{
		"127.0.0.1:8787": "127.0.0.1",
		"127.12.4.9:443": "127.12.4.9",
		"[::1]:8787":     "::1",
	}
	for address, expectedHost := range valid {
		host, err := validateListenAddress(address)
		if err != nil {
			t.Errorf("validateListenAddress(%q): %v", address, err)
		} else if host != expectedHost {
			t.Errorf("validateListenAddress(%q) host = %q, want %q", address, host, expectedHost)
		}
	}
	invalid := []string{
		"0.0.0.0:8787",
		"[::]:8787",
		"192.168.1.2:8787",
		"localhost:8787",
		"127.0.0.1:0",
		"127.0.0.1:-1",
		"127.0.0.1:65536",
		"127.0.0.1",
		"::1:8787",
	}
	for _, address := range invalid {
		if _, err := validateListenAddress(address); err == nil {
			t.Errorf("validateListenAddress(%q) unexpectedly succeeded", address)
		}
	}
}
