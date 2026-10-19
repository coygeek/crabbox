package freestyle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

// The wire fixtures follow https://api.freestyle.sh/ (v5), rather than
// marshaling the client's own types, so legacy fields cannot pass unnoticed.
func TestFreestyleCurrentAPILifecycle(t *testing.T) {
	const name = "crabbox-repo-abc123"
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing bearer authentication")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v5/vms":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, field := range []string{"name", "ports", "template"} {
				if _, ok := body[field]; ok {
					t.Errorf("legacy create field %q", field)
				}
			}
			if string(body["displayName"]) != `"`+name+`"` || string(body["slug"]) != `"`+name+`"` {
				t.Errorf("ownership names: %s", body)
			}
			var firewall struct {
				Rules []struct {
					Action      string
					Source      map[string]any
					Destination map[string]any
				}
			}
			if err := json.Unmarshal(body["firewall"], &firewall); err != nil {
				t.Error(err)
			}
			if len(firewall.Rules) != 1 || firewall.Rules[0].Action != "allow" || len(firewall.Rules[0].Source) != 0 || firewall.Rules[0].Destination["public"] != true {
				t.Errorf("want outbound Internet only: %s", body["firewall"])
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"vm123","displayName":"`+name+`","slug":"`+name+`","state":"starting"}`)
		case "GET /v5/vms/vm123":
			io.WriteString(w, `{"id":"vm123","displayName":"`+name+`","slug":"`+name+`","state":"running"}`)
		case "GET /v5/vms":
			if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("offset") != "0" {
				t.Errorf("query=%s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"vms":[{"id":"vm123","displayName":"`+name+`","slug":"`+name+`","state":"running"}],"totalCount":1}`)
		case "POST /v5/vms/vm123/exec-await":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["command"] != "printf test" {
				t.Errorf("command=%v", body)
			}
			io.WriteString(w, `{"stdout":"test","stderr":"diagnostic","statusCode":7}`)
		case "DELETE /v5/vms/vm123":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "legacy or unknown route", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := &freestyleHTTPClient{apiKey: "test-key", apiURL: server.URL, httpClient: server.Client()}
	created, err := client.CreateVM(t.Context(), freestyleCreateVMRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "vm123" || created.Name != name || created.State != "starting" {
		t.Fatalf("create=%+v", created)
	}
	vm, err := client.GetVM(t.Context(), created.ID)
	if err != nil || vm.Name != name || !isCrabboxFreestyleSandboxName(vm.Name) {
		t.Fatalf("get=%+v err=%v", vm, err)
	}
	vms, err := client.ListVMs(t.Context())
	if err != nil || len(vms) != 1 || vms[0].Name != name {
		t.Fatalf("list=%+v err=%v", vms, err)
	}
	var stdout, stderr bytes.Buffer
	code, err := client.Exec(t.Context(), created.ID, "printf test", &stdout, &stderr)
	if err != nil || code != 7 || stdout.String() != "test" || stderr.String() != "diagnostic" {
		t.Fatalf("exec=%d %v %q %q", code, err, &stdout, &stderr)
	}
	if err := client.DeleteVM(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Errorf("requests=%v", seen)
	}
}

func TestFreestyleCurrentAPIBinaryFileTransport(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/v5/vms/vm123/exec-await" {
			http.Error(w, "no file endpoint", 404)
			return
		}
		var body struct {
			Command string `json:"command"`
			Stdin   string `json:"stdin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		stdin, err := base64.StdEncoding.DecodeString(body.Stdin)
		if err != nil || len(stdin) > 1<<20 {
			t.Errorf("invalid stdin: size=%d err=%v", len(stdin), err)
			return
		}
		cmd := exec.CommandContext(r.Context(), "bash", "-lc", body.Command)
		cmd.Stdin = bytes.NewReader(stdin)
		var out, errs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errs
		code := 0
		if err := cmd.Run(); err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Error(err)
				code = 1
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"statusCode": code, "stdout": out.String(), "stderr": errs.String()})
	}))
	defer server.Close()
	client := &freestyleHTTPClient{apiKey: "test-key", apiURL: server.URL, httpClient: server.Client()}
	file := filepath.Join(t.TempDir(), "space and 'quote' $literal.bin")
	content := bytes.Repeat([]byte{0, 0xff, 0x80, 13, 10, 42}, 200000) // exceeds one stdin request
	if err := client.WriteFile(t.Context(), "vm123", file, base64.StdEncoding.EncodeToString(content), "base64"); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(disk, content) {
		t.Fatalf("write bytes=%d err=%v", len(disk), err)
	}
	read, err := client.ReadFile(t.Context(), "vm123", file)
	if err != nil || !bytes.Equal([]byte(read), content) {
		t.Fatalf("read bytes=%d err=%v", len(read), err)
	}
	if requests < 3 {
		t.Errorf("requests=%d: large input must be chunked", requests)
	}
	if err := client.WriteFile(context.Background(), "vm123", file, "", "utf8"); err != nil {
		t.Fatal(err)
	}
	disk, err = os.ReadFile(file)
	if err != nil || len(disk) != 0 {
		t.Fatalf("empty write bytes=%d err=%v", len(disk), err)
	}
	_, err = client.ReadFile(t.Context(), "vm123", file+".missing")
	if err == nil || !strings.Contains(err.Error(), "read file") {
		t.Fatalf("missing file err=%v", err)
	}
}

func TestFreestyleCurrentAPISlugOwnershipFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vm := `{"id":"vm123","displayName":null,"slug":"crabbox-repo-abc123","state":"running"}`
		if r.URL.Path == "/v5/vms" {
			io.WriteString(w, `{"vms":[`+vm+`],"totalCount":1}`)
		} else {
			io.WriteString(w, vm)
		}
	}))
	defer server.Close()
	client := &freestyleHTTPClient{apiKey: "test-key", apiURL: server.URL, httpClient: server.Client()}
	vm, err := client.GetVM(t.Context(), "vm123")
	if err != nil || vm.Name != "crabbox-repo-abc123" || !isCrabboxFreestyleSandboxName(vm.Name) {
		t.Fatalf("get=%+v err=%v", vm, err)
	}
	vms, err := client.ListVMs(t.Context())
	if err != nil || len(vms) != 1 || vms[0].Name != vm.Name {
		t.Fatalf("list=%+v err=%v", vms, err)
	}
}

func TestFreestyleCurrentAPISandboxNameFitsSlug(t *testing.T) {
	for _, repo := range []string{"repo", strings.Repeat("long-repo-", 20), "Crabbox / ."} {
		name := newFreestyleSandboxName(core.Repo{Name: repo})
		if len(name) > 63 || !isCrabboxFreestyleSandboxName(name) {
			t.Errorf("invalid v5 slug %q", name)
		}
	}
}

func TestFreestyleCurrentAPIWaitBeforeClaim(t *testing.T) {
	for _, state := range []string{"running", "stopped"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			client := &fakeFreestyleClient{createID: "vm-starting", createState: "starting", getVM: freestyleVM{ID: "vm-starting", State: state}}
			backend := &freestyleBackend{rt: core.Runtime{Stderr: io.Discard}}
			lease, _, _, err := backend.createSandbox(t.Context(), client, core.Repo{Root: t.TempDir(), Name: "repo"}, false, "")
			if state == "running" {
				if err != nil || lease != "fsb_vm-starting" || len(client.deleteIDs) != 0 {
					t.Fatalf("lease=%s err=%v deletes=%v", lease, err, client.deleteIDs)
				}
			} else {
				if err == nil || len(client.deleteIDs) != 1 || client.deleteIDs[0] != "vm-starting" {
					t.Fatalf("err=%v deletes=%v", err, client.deleteIDs)
				}
				if _, found, claimErr := core.ReadLeaseClaimWithPresence("fsb_vm-starting"); claimErr != nil || found {
					t.Fatalf("claim=%t err=%v", found, claimErr)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &fakeFreestyleClient{getVM: freestyleVM{State: "starting"}}
	if err := waitForFreestyleVM(ctx, client, "vm-starting"); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation=%v", err)
	}
}
