package notify

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSend_Bel(t *testing.T) {
	output := captureStderr(t, func() {
		Send("bel", "ignored")
	})
	assert.Equal(t, "\a", output)
}

func TestSend_OSC9(t *testing.T) {
	output := captureStderr(t, func() {
		Send("osc9", "hello world")
	})
	assert.Equal(t, "\033]9;hello world\033\\", output)
}

func TestSend_Off(t *testing.T) {
	output := captureStderr(t, func() {
		Send("off", "ignored")
	})
	assert.Empty(t, output)
}

func TestSend_Unknown(t *testing.T) {
	output := captureStderr(t, func() {
		Send("unknown", "ignored")
	})
	assert.Empty(t, output)
}

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name           string
		merged         int
		skipped        int
		pipelineStatus string
		want           string
	}{
		{
			name:           "all merged with pipeline",
			merged:         3,
			skipped:        0,
			pipelineStatus: "success",
			want:           "glmt: 3 merged | pipeline success",
		},
		{
			name:           "mixed with pipeline",
			merged:         2,
			skipped:        1,
			pipelineStatus: "failed",
			want:           "glmt: 2 merged, 1 skipped | pipeline failed",
		},
		{
			name:           "no pipeline status",
			merged:         1,
			skipped:        0,
			pipelineStatus: "",
			want:           "glmt: 1 merged",
		},
		{
			name:           "none merged",
			merged:         0,
			skipped:        2,
			pipelineStatus: "",
			want:           "glmt: 2 skipped",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatMessage(tt.merged, tt.skipped, tt.pipelineStatus)
			assert.Equal(t, tt.want, got)
		})
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	fn()

	require.NoError(t, w.Close())
	os.Stderr = old

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	require.NoError(t, r.Close())
	return string(buf[:n])
}
