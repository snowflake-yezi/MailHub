package main

import "testing"

func TestClampHeartbeat(t *testing.T) {
	tests := []struct {
		v, fallback, want int
	}{
		{30, 60, 30},   // 合法值原样返回
		{5, 60, 5},     // 下界
		{600, 60, 600}, // 上界
		{0, 60, 60},    // 非法 → fallback
		{-1, 60, 60},   // 负值 → fallback
		{4, 60, 60},    // 下界以下 → fallback
		{601, 60, 60},  // 上界以上 → fallback
	}
	for _, tc := range tests {
		if got := clampHeartbeat(tc.v, tc.fallback); got != tc.want {
			t.Fatalf("clampHeartbeat(%d, %d) = %d, want %d", tc.v, tc.fallback, got, tc.want)
		}
	}
}
