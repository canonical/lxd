package main

import (
	"testing"
)

func Test_generateRandomString(t *testing.T) {
	str1, err1 := generateRandomString(10)
	str2, err2 := generateRandomString(10)

	if err1 != nil {
		t.Errorf("Error generating string 1: %v", err1)
	}

	if err2 != nil {
		t.Errorf("Error generating string 2: %v", err2)
	}

	if len(str1) != 10 {
		t.Errorf("Expected length 10, got %d", len(str1))
	}

	if len(str2) != 10 {
		t.Errorf("Expected length 10, got %d", len(str2))
	}

	if str1 == str2 {
		t.Errorf("Expected different strings, got identical: %s", str1)
	}
}

func Benchmark_generateRandomString(b *testing.B) {
	for b.Loop() {
		_, _ = generateRandomString(10)
	}
}
