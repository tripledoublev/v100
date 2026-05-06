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
}
