package providers

import (
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestBuildOpenAIChatMessagesWithImage(t *testing.T) {
	msgs := buildOpenAIChatMessages([]Message{
		{
			Role:    "user",
			Content: "What is in this image?",
			Images: []ImageAttachment{{
				MIMEType: "image/png",
				Data:     []byte{0x89, 0x50, 0x4e, 0x47},
			}},
		},
	})

	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	if msgs[0].Content != "" {
		t.Fatalf("expected empty string content when MultiContent is used, got %q", msgs[0].Content)
	}
	if len(msgs[0].MultiContent) != 2 {
		t.Fatalf("part count = %d, want 2", len(msgs[0].MultiContent))
	}
	if msgs[0].MultiContent[0].Type != openai.ChatMessagePartTypeText {
		t.Fatalf("expected first part text, got %#v", msgs[0].MultiContent[0])
	}
	if msgs[0].MultiContent[1].Type != openai.ChatMessagePartTypeImageURL {
		t.Fatalf("expected second part image_url, got %#v", msgs[0].MultiContent[1])
	}
	if msgs[0].MultiContent[1].ImageURL == nil {
		t.Fatal("expected image_url payload")
	}
	if !strings.HasPrefix(msgs[0].MultiContent[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("unexpected image data URL: %q", msgs[0].MultiContent[1].ImageURL.URL)
	}
}
