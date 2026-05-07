package main

import "testing"

func TestLexiconCmdRegistered(t *testing.T) {
	cmd := rootCmd()
	child, _, err := cmd.Find([]string{"lexicon", "publish-provenance"})
	if err != nil || child == nil || child.Name() != "publish-provenance" {
		t.Fatalf("publish-provenance command not registered: child=%v err=%v", child, err)
	}
}

func TestProvenanceLexiconRecordShape(t *testing.T) {
	record := provenanceLexiconRecord()
	if record["$type"] != "com.atproto.lexicon.schema" {
		t.Fatalf("$type = %v", record["$type"])
	}
	if record["id"] != "art.xx-c.provenance" {
		t.Fatalf("id = %v", record["id"])
	}
	defs := record["defs"].(map[string]any)
	main := defs["main"].(map[string]any)
	if main["type"] != "record" || main["key"] != "tid" {
		t.Fatalf("main def = %#v", main)
	}
	body := main["record"].(map[string]any)
	required := body["required"].([]string)
	if !containsString(required, "subject") || containsString(required, "post") {
		t.Fatalf("required = %#v, want subject and no post", required)
	}
	props := body["properties"].(map[string]any)
	subject := props["subject"].(map[string]any)
	if subject["type"] != "ref" || subject["ref"] != "com.atproto.repo.strongRef" {
		t.Fatalf("subject = %#v, want strongRef", subject)
	}
	sources := props["sources"].(map[string]any)
	sourceItem := sources["items"].(map[string]any)
	if sourceItem["type"] != "ref" || sourceItem["ref"] != "com.atproto.repo.strongRef" {
		t.Fatalf("source item = %#v, want strongRef", sourceItem)
	}
}

func TestPublishedRecordRepoDID(t *testing.T) {
	got := publishedRecordRepoDID(`{"ok":true,"repo":"did:plc:365qanyf6vasnrsr2zppc66t"}`)
	if got != "did:plc:365qanyf6vasnrsr2zppc66t" {
		t.Fatalf("repo DID = %q", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
