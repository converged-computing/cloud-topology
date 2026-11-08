package database

import "testing"

func TestCloudOf(t *testing.T) {
	for name, want := range map[string]string{"c7i.48xlarge": "aws", "u-6tb1.112xlarge": "aws", "n2-standard-8": "google-cloud", "a3-highgpu-8g": "google-cloud"} {
		if got := CloudOf(name); got != want {
			t.Errorf("CloudOf(%q) = %q, want %q", name, got, want)
		}
	}
}
