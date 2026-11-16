package testsources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
	"testing"
)

func TestPrivateInputSuite(t *testing.T) {
	store := objectstore.NewMemoryStore("private")
	payload := []byte(`{"schema_version":"sandbox.inputs.v1","suite_id":"39b81f29-9d03-4b1a-bdce-af9931eea612","version":"1","inputs":["hello","secret"]}`)
	ref := contracts.InputSuiteReference{SuiteID: "39b81f29-9d03-4b1a-bdce-af9931eea612", Version: "1", ChecksumSHA256: SHA256Hex(payload)}
	key := "suites/" + ref.SuiteID + "/1/suite.json"
	if _, err := store.Put(context.Background(), objectstore.Object{Bucket: "private", Key: key, Content: payload}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(NewObjectStorageProvider(config.ObjectStorageTestsConfig{Bucket: "private", KeyPrefix: "suites", MaxBytes: 1024}, store))
	req := contracts.RunRequest{Execution: &contracts.ExecutionSpec{Mode: "execution", Suite: &ref}}
	resolved, err := resolver.Resolve(context.Background(), req)
	if err != nil || len(resolved.Execution.Inputs) != 2 || resolved.Execution.Suite != nil || !resolved.PrivateInputs || contracts.DiagnosticsVisibilityForRequest(resolved) != "private" {
		t.Fatalf("resolve: %v", err)
	}
	for _, kind := range []string{"checksum", "version", "unknown", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			changed := ref
			data := append([]byte{}, payload...)
			switch kind {
			case "checksum":
				changed.ChecksumSHA256 = SHA256Hex([]byte("bad"))
			case "version":
				data = []byte(`{"schema_version":"sandbox.inputs.v1","suite_id":"39b81f29-9d03-4b1a-bdce-af9931eea612","version":"2","inputs":["hello"]}`)
			case "unknown":
				var obj map[string]any
				_ = json.Unmarshal(data, &obj)
				obj["expected"] = "hidden"
				data, _ = json.Marshal(obj)
			case "oversize":
				data = make([]byte, 1025)
			}
			if kind != "checksum" {
				changed.ChecksumSHA256 = SHA256Hex(data)
			}
			_, _ = store.Put(context.Background(), objectstore.Object{Bucket: "private", Key: key, Content: data})
			req.Execution = &contracts.ExecutionSpec{Mode: "execution", Suite: &changed}
			if _, err := resolver.Resolve(context.Background(), req); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestSharedGradedFixture(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/execution-contract/graded.request.json")
	if err != nil {
		t.Fatal(err)
	}
	var req contracts.RunRequest
	if err = json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if err = contracts.ValidateRunRequest(req); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../tests/fixtures/execution-contract/input-suite.json")
	if err != nil {
		t.Fatal(err)
	}
	store := objectstore.NewMemoryStore("private")
	ref := req.Execution.Suite
	if _, err = store.Put(context.Background(), objectstore.Object{Bucket: "private", Key: ref.SuiteID + "/" + ref.Version + "/suite.json", Content: manifest}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(NewObjectStorageProvider(config.ObjectStorageTestsConfig{Bucket: "private", MaxBytes: 4096}, store))
	resolved, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err = contracts.ValidateRunRequest(resolved); err != nil {
		t.Fatal(err)
	}
	resultContext := contracts.EvaluationContextForRequest(resolved)
	if resultContext == nil || resultContext.TaskVersionID != req.Metadata["task_version_id"] || resultContext.SuiteSHA256 != contracts.InputSuiteSHA256([]string{"hello", "private-fixture-input"}) {
		t.Fatal("resolved context does not prove inputs")
	}
}

func TestPrivateSuiteRejectsUnpinnedAndUnsafeRefs(t *testing.T) {
	store := &countingStore{inner: objectstore.NewMemoryStore("private")}
	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{Bucket: "private", KeyPrefix: "suites"}, store)
	for _, version := range []string{"", "latest", "../1", "1/../../outside", "https://foreign"} {
		req := contracts.RunRequest{Execution: &contracts.ExecutionSpec{Mode: "execution", Suite: &contracts.InputSuiteReference{SuiteID: "39b81f29-9d03-4b1a-bdce-af9931eea612", Version: version, ChecksumSHA256: SHA256Hex(nil)}}}
		if _, err := provider.ResolveInputs(context.Background(), req); err == nil {
			t.Fatalf("accepted %q", version)
		}
	}
	if store.getCalls != 0 {
		t.Fatal("unsafe ref fetched")
	}
}

func TestSuiteRejectsTraversalFilesAndDecodedBounds(t *testing.T) {
	for _, kind := range []string{"traversal", "duplicate", "files", "bytes"} {
		suite := validSuite()
		switch kind {
		case "traversal":
			suite.Files[0].Path = "dir/../hidden.py"
		case "duplicate":
			suite.Files = append(suite.Files, suite.Files[0])
		case "files":
			suite.Files = make([]contracts.SourceFile, 33)
		case "bytes":
			suite.Files[0].ContentB64 = base64.StdEncoding.EncodeToString(make([]byte, 1048577))
		}
		if err := ValidateSuite(suite); err == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
}
