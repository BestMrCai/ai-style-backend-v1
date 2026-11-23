package ai

import (
	"context"
	"testing"
)

func TestMockGeneratorGenerate(t *testing.T) {
	generator := NewMockGenerator()

	tests := []struct {
		name       string
		designType string
	}{
		{
			name:       "hairstyle design",
			designType: "hairstyle",
		},
		{
			name:       "outfit design",
			designType: "outfit",
		},
		{
			name:       "full style design",
			designType: "full_style",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generator.Generate(
				context.Background(),
				GenerateRequest{
					Portrait:   []byte("test image"),
					DesignType: tt.designType,
					DesignMode: "auto",
				},
			)

			if err != nil {
				t.Fatalf("Generate returned error: %v", err)
			}

			if result == nil {
				t.Fatal("Generate returned nil result")
			}

			if result.Suggestion == nil {
				t.Fatal("Generate returned empty suggestion")
			}
		})
	}
}

func TestMockGeneratorRejectsEmptyPortrait(t *testing.T) {
	generator := NewMockGenerator()

	_, err := generator.Generate(
		context.Background(),
		GenerateRequest{
			DesignType: "hairstyle",
		},
	)

	if err == nil {
		t.Fatal("expected error for empty portrait")
	}
}

func TestMockGeneratorRespectsCancelledContext(t *testing.T) {
	generator := NewMockGenerator()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := generator.Generate(
		ctx,
		GenerateRequest{
			Portrait:   []byte("test image"),
			DesignType: "hairstyle",
		},
	)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
