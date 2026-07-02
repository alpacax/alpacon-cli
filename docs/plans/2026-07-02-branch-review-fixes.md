# 201-large-exec 리뷰 findings 수정 계획 (브랜치 연관분만)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 6단계 clean-review-loop findings 중 이 브랜치가 새로 만든 코드에서 비롯된 것만 수정한다. 핵심은 oversized 판정을 cmd 레이어 프리플라이트에서 `api/event.SubmitCommand`(sink)로 옮겨 파라미터 스레딩·중복 네트워크 조회·재시도 인프라 이탈을 한 번에 해소하는 것.

**Architecture:** 서버의 list 엔드포인트(`GET /api/servers/servers/?name=X`)는 `ServerListSerializer`를 사용하며 `platform` 필드를 포함한다(alpacon-server `servers/api/serializers.py:249`, `views.py:179` 확인 완료). 따라서 `SubmitCommand`가 이미 수행하는 이름→ID 조회 한 번으로 platform까지 얻을 수 있고, oversized 판정을 그 안에서 하면 추가 round-trip이 0이 된다. `cmd/exec/oversized.go`와 `api/server.GetServerPlatform`은 통째로 삭제되고, 6개 시그니처의 `oversized bool` 파라미터가 원복된다.

**Tech Stack:** Go 1.25, Cobra, testify, httptest.

## Global Constraints

- 커밋은 `/alpacax-lore-commit` 규약(Conventional Commits + Lore 트레일러)으로 메인 에이전트가 수행. 아래 커밋 스텝의 subject/body를 기반으로 트레일러는 커밋 시점에 작성.
- 이 계획 문서(`docs/plans/`)는 절대 커밋하지 않는다.
- Go 선언 순서 `const → var → type → func`. 주석은 영어, 1줄, "why" 중심.
- 에러 문자열은 소문자로 시작, 끝 문장부호 없음 (staticcheck ST1005).
- 종료 코드 0/1/2/3/4 의미 불변.
- 워킹트리에 이미 미커밋 수정 2건 존재: `api/event/types.go`의 `Oversized` 필드 주석(유지, Task 3 커밋에 포함), `cmd/exec/oversized.go`의 const 블록(파일 자체가 Task 3에서 삭제되므로 소멸).

## 반영하는 findings

| # | Finding | 해소 방법 |
|---|---------|----------|
| 1 | [S5 CRITICAL] `oversized` bool이 6개 시그니처+테스트 15곳 관통 | Task 2–3: sink 이동, 시그니처 원복 |
| 2 | [S2] oversized 경로 이름→ID 이중 resolve (3 round-trip) | Task 1+3: `GetServerByName` 단일 조회로 0 extra RT |
| 3 | [S2] `GetServerPlatform`이 2 call | Task 1+3: list 응답의 platform 사용, 함수 삭제 |
| 4 | [S1] 플랫폼 조회가 MFA/토큰갱신 재시도 밖 | Task 3: 판정이 `SubmitCommand` 안으로 → RetryOperation 커버 |
| 5 | [S4/S5/S6] `cmd/exec/exec.go:112`·`cmd/websh/websh.go:228` 중복 주석 | Task 3: 콜사이트 삭제로 소멸 |
| 6 | [S5] `exceedsInlineLimit` 1줄 래퍼, `windowsPlatform` 1회용 상수 | Task 2–3: `resolveOversized`로 흡수, `"windows"` 인라인 |
| 7 | [S2] `GetEventList` 전체 flatten 후 truncate | Task 4: `previewForTable`로 사전 바운딩 |
| 8 | [S3] 제어문자(ANSI escape) 미소독 테이블 출력 | Task 4: 동일 헬퍼에서 control rune 제거 |
| 9 | [S4] `assert.True(t, present && got == true)` 복합 단언 | Task 3: wire 테스트 재작성에 흡수 |
| 10 | [S6] 신규 `TestGetServerPlatform` stdlib 단언 | Task 1+3: 대체 테스트를 testify로 작성, 기존 것 삭제 |

## 제외한 findings (브랜치 무관 — pre-existing)

- **`ErrorHandlerCallbacks` 중복** (S4): merge-base `0473b0f`의 `cmd/exec/exec.go:120`에 이미 존재하던 블록을 브랜치가 `runDetached`로 위치만 이동. 중복 자체는 기존 문제.
- **파라미터 체인 struct 리팩터** (S4): 8-파라미터 체인은 브랜치 이전부터 존재. 본 계획이 `oversized`를 제거하면 브랜치 이전 형태로 원복됨.
- **서버측 oversized 검증 확인** (S3): CLI 코드 변경 아님. alpacon-server 교차 검증 완료(2048 제한·일관성 검증은 serializer가 소유) — 종결.

---

## Phase 1 — api/server: 단일 조회로 전체 detail 반환

### Task 1: `GetServerByName` 추가

**Files:**
- Modify: `api/server/server.go:97-117` (`GetServerIDByName` 위에 추가, 본체는 위임으로 축소)
- Test: `api/server/server_test.go` (파일 끝에 추가)

**Interfaces:**
- Produces: `func GetServerByName(ac *client.AlpaconClient, serverName string) (ServerDetails, error)` — Task 3의 `SubmitCommand`가 사용. `ServerDetails.ID`, `ServerDetails.Platform` 필드 사용.

- [ ] **Step 1: 실패하는 테스트 작성**

`api/server/server_test.go` 끝에 추가 (import에 `github.com/stretchr/testify/assert`, `github.com/stretchr/testify/require` 추가):

```go
func TestGetServerByName(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "my-server", r.URL.Query().Get("name"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ServerDetails]{
			Count:   1,
			Results: []ServerDetails{{ID: "srv-1", Name: "my-server", Platform: "windows"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	srv, err := GetServerByName(ac, "my-server")
	require.NoError(t, err)
	assert.Equal(t, "srv-1", srv.ID)
	assert.Equal(t, "windows", srv.Platform)
}

func TestGetServerByName_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ServerDetails]{Count: 0})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetServerByName(ac, "ghost")
	require.Error(t, err)
}
```

- [ ] **Step 2: 실패 확인**

Run: `go test ./api/server/ -run TestGetServerByName -v`
Expected: FAIL — `undefined: GetServerByName` (컴파일 에러)

- [ ] **Step 3: 최소 구현**

`api/server/server.go`의 `GetServerIDByName`(97-117행)을 다음으로 교체:

```go
// GetServerByName resolves a server name to its full list projection in one lookup.
func GetServerByName(ac *client.AlpaconClient, serverName string) (ServerDetails, error) {
	params := map[string]string{
		"name": serverName,
	}
	body, err := ac.SendGetRequest(utils.BuildURL(serverURL, "", params))
	if err != nil {
		return ServerDetails{}, err
	}

	var response api.ListResponse[ServerDetails]
	if err := json.Unmarshal(body, &response); err != nil {
		return ServerDetails{}, err
	}

	if response.Count == 0 {
		return ServerDetails{}, errors.New("no server found with the given name")
	}

	return response.Results[0], nil
}

func GetServerIDByName(ac *client.AlpaconClient, serverName string) (string, error) {
	srv, err := GetServerByName(ac, serverName)
	if err != nil {
		return "", err
	}
	return srv.ID, nil
}
```

(`GetServerIDByName`은 콜러가 많아 위임 래퍼로 유지—전 콜사이트 수정 회피가 래퍼의 존재 이유.)

- [ ] **Step 4: 통과 확인**

Run: `go test ./api/server/ -v`
Expected: PASS (신규 2개 포함 전체)

- [ ] **Step 5: 커밋**

```bash
git add api/server/server.go api/server/server_test.go
```

Subject: `refactor(server): resolve server name to full detail in one lookup`
Body 요지: list 응답이 이미 platform을 포함하므로(ServerListSerializer) 이름 조회 한 번으로 ID+platform을 얻는 `GetServerByName`을 추가하고 `GetServerIDByName`을 위임으로 축소. 후속 커밋에서 oversized 판정이 이 단일 조회를 재사용한다.

---

## Phase 2 — api/event: oversized 판정을 sink로 이동

### Task 2: `resolveOversized` 순수 함수

**Files:**
- Create: `api/event/oversized.go`
- Test: `api/event/oversized_test.go` (신규)

**Interfaces:**
- Produces: `func resolveOversized(command, platform string) (bool, error)` — Task 3의 `SubmitCommand`가 사용. `const inlineCommandLimit = 2048`.

- [ ] **Step 1: 실패하는 테스트 작성**

`api/event/oversized_test.go` 생성:

```go
package event

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOversized(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		platform  string
		oversized bool
		wantErr   bool
	}{
		{"at limit stays inline", strings.Repeat("a", 2048), "linux", false, false},
		{"one over limit is oversized", strings.Repeat("a", 2049), "linux", true, false},
		{"multibyte counts bytes", strings.Repeat("가", 683), "linux", true, false},
		{"windows oversized is an error", strings.Repeat("a", 2049), "windows", false, true},
		{"windows inline is fine", "dir", "windows", false, false},
		{"unknown platform allowed", strings.Repeat("a", 2049), "", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOversized(tt.command, tt.platform)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.oversized, got)
		})
	}
}
```

(`가`는 UTF-8 3바이트 → 683자 = 2049바이트. 바이트 기준 계약 검증.)

- [ ] **Step 2: 실패 확인**

Run: `go test ./api/event/ -run TestResolveOversized -v`
Expected: FAIL — `undefined: resolveOversized`

- [ ] **Step 3: 최소 구현**

`api/event/oversized.go` 생성:

```go
package event

import "errors"

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// resolveOversized reports whether command must be staged as a temp script
// (byte-based; may flag before the server's char limit, never after). Windows
// has no sh wrapper, so an oversized command there is an error.
func resolveOversized(command, platform string) (bool, error) {
	if len(command) <= inlineCommandLimit {
		return false, nil
	}
	if platform == "windows" {
		return false, errors.New("oversized commands are not supported on Windows servers")
	}
	return true, nil
}
```

- [ ] **Step 4: 통과 확인**

Run: `go test ./api/event/ -run TestResolveOversized -v`
Expected: PASS (6 서브테스트)

- [ ] **Step 5: 커밋 없음** — Task 3과 하나의 논리 단위이므로 Task 3에서 함께 커밋.

### Task 3: `SubmitCommand` 통합 + 시그니처 원복 + cmd 레이어 정리

**Files:**
- Modify: `api/event/event.go:76-118` (`SubmitCommand`, `RunCommand`)
- Modify: `api/event/event_test.go` (콜사이트 12곳 + wire 테스트 재작성 + Windows 거부 테스트 추가)
- Modify: `cmd/exec/run.go:113-142, 171-211, 262-281, 366-382` (시그니처·콜사이트 원복)
- Modify: `cmd/exec/exec.go:110-122` (`ResolveOversized` 호출·주석 삭제)
- Modify: `cmd/websh/websh.go:227-230` (주석·`ResolveOversized` 호출 삭제)
- Delete: `cmd/exec/oversized.go`, `cmd/exec/oversized_test.go`
- Modify: `api/server/server.go:68-81` (`GetServerPlatform` 삭제), `api/server/server_test.go` (`TestGetServerPlatform` 삭제)

**Interfaces:**
- Consumes: Task 1의 `server.GetServerByName`, Task 2의 `resolveOversized`.
- Produces (원복된 시그니처 — 이후 유일한 형태):
  - `event.SubmitCommand(ac, serverName, command, username, groupname string, env map[string]string, workSessionID string) (CommandResponse, error)`
  - `event.RunCommand(ac, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error)`
  - `exec.RunCommandWithRetry(ac, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error)`
  - `exec.RunExecWithPresenceStepUp(...같은 형태...)`, `exec.RunExecWithApprovalWait(..., wait bool)`
  - `runDetached(ac, parsed, env, workSessionID, authMethod)`

- [ ] **Step 1: wire 테스트를 새 계약으로 재작성 (RED)**

`api/event/event_test.go`의 `TestSubmitCommand_OversizedFlagOnWire`(566-610행 부근)를 다음으로 교체 — oversized는 파라미터가 아니라 커맨드 길이로 유도, 복합 단언 분리(finding 9):

```go
func TestSubmitCommand_OversizedFlagOnWire(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		oversized bool
	}{
		{"inline omits flag", "echo hi", false},
		{"oversized sets flag", strings.Repeat("a", 2049), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/"):
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
						Count:   1,
						Results: []map[string]any{{"id": "srv-1", "name": "server-x", "platform": "debian"}},
					})
				case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/events/commands/"):
					_ = json.NewDecoder(r.Body).Decode(&body)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "job-1"}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := SubmitCommand(ac, "server-x", tt.command, "", "", nil, "")
			require.NoError(t, err)

			// omitempty: the field is absent on the wire when false.
			got, present := body["oversized"]
			if tt.oversized {
				require.True(t, present, "oversized must be present on the wire")
				assert.Equal(t, true, got)
			} else {
				assert.False(t, present, "oversized must be omitted when false")
			}
		})
	}
}

func TestSubmitCommand_OversizedWindowsRejected(t *testing.T) {
	var posted bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count:   1,
				Results: []map[string]any{{"id": "srv-1", "name": "server-x", "platform": "windows"}},
			})
		case r.Method == http.MethodPost:
			posted = true
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := SubmitCommand(ac, "server-x", strings.Repeat("a", 2049), "", "", nil, "")
	require.Error(t, err)
	assert.False(t, posted, "command must not be submitted to a Windows server")
}
```

- [ ] **Step 2: 실패 확인 (컴파일 에러)**

Run: `go test ./api/event/ -run TestSubmitCommand -v`
Expected: FAIL — `too many arguments`/`not enough arguments` 류 컴파일 에러 (신·구 시그니처 혼재)

- [ ] **Step 3: `SubmitCommand`/`RunCommand` 구현 변경**

`api/event/event.go` 76-104행을 다음으로 교체:

```go
func SubmitCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID string) (CommandResponse, error) {
	srv, err := server.GetServerByName(ac, serverName)
	if err != nil {
		return CommandResponse{}, err
	}
	oversized, err := resolveOversized(command, srv.Platform)
	if err != nil {
		return CommandResponse{}, err
	}
	commandRequest := &CommandRequest{
		Shell:       "system",
		Line:        command,
		Env:         env,
		Username:    username,
		Groupname:   groupname,
		Server:      srv.ID,
		RunAfter:    []string{},
		WorkSession: workSessionID,
		Oversized:   oversized,
	}
	respBody, err := ac.SendPostRequest(getEventURL, commandRequest)
	if err != nil {
		return CommandResponse{}, err
	}
	var cmdResponse []CommandResponse
	if err = json.Unmarshal(respBody, &cmdResponse); err != nil {
		return CommandResponse{}, err
	}
	if len(cmdResponse) == 0 {
		return CommandResponse{}, fmt.Errorf("server returned empty command list")
	}
	return cmdResponse[0], nil
}
```

`RunCommand`(106-118행)의 시그니처에서 `, oversized bool` 제거, 내부 `SubmitCommand` 호출에서 `, oversized` 제거.

- [ ] **Step 4: cmd 레이어 원복**

`cmd/exec/run.go` — 아래 5곳에서 시그니처의 `, oversized bool`(RunExecWithApprovalWait는 `wait, oversized bool` → `wait bool`)과 모든 콜사이트의 `, oversized`를 제거:
- `RunExecWithPresenceStepUp` (113행) + 내부 호출 2곳 (114, 142행)
- `RunExecWithApprovalWait` (171행) + 내부 호출 2곳 (172, 211행)
- `RunCommandWithRetry` (262행) + `event.RunCommand` 호출 2곳 (263, 281행)
- `runDetached` (366행, `oversized bool` 파라미터 제거) + `event.SubmitCommand` 호출 2곳 (367, 382행)

`cmd/exec/exec.go` — 110-122행에서 다음을 삭제/수정:

```go
		env := make(map[string]string)

		if parsed.Detach {
			runDetached(alpaconClient, parsed, env, workSessionID, authMethod)
			return
		}

		result, err := RunExecWithApprovalWait(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Wait)
```

(3줄 주석과 `oversized := ResolveOversized(...)` 줄 삭제 — finding 5.)

`cmd/websh/websh.go` — 227-230행을 다음으로 교체:

```go
		if len(commandArgs) > 0 {
			command := execCmd.ShellJoin(commandArgs)
			result, err := execCmd.RunCommandWithRetry(alpaconClient, serverName, command, username, groupname, env, workSessionID)
```

파일 삭제:

```bash
git rm cmd/exec/oversized.go cmd/exec/oversized_test.go
```

`api/server/server.go` — `GetServerPlatform`(68-81행) 삭제 (유일 콜러가 사라짐). `api/server/server_test.go` — `TestGetServerPlatform` 삭제 (Task 1의 `TestGetServerByName`이 platform 파싱을 대체 검증).

- [ ] **Step 5: 나머지 테스트 콜사이트 원복**

`api/event/event_test.go`의 11곳(303, 319, 334, 348, 365, 437, 494, 562, 680, 694, 707행)에서 마지막 인자 `, false` 제거:

```go
_, err := RunCommand(ac, "server-x", "ls", "", "", nil, "ses-abc")
```

(562행 `SubmitCommand`도 동일하게 `, false` 제거.)

- [ ] **Step 6: 전체 빌드·테스트 통과 확인**

Run: `go build ./... && go vet ./... && go test -race ./api/... ./cmd/...`
Expected: 빌드·vet 무출력, 테스트 전체 PASS (신규 `TestSubmitCommand_OversizedWindowsRejected` 포함)

주의: 기존 `RunCommand` 계열 테스트의 httptest 핸들러는 servers list 응답에 platform이 없어도 통과해야 정상 (빈 platform → 비Windows 취급, 커맨드가 짧아 inline). 실패 시 핸들러가 아닌 구현을 의심할 것.

- [ ] **Step 7: 커밋**

```bash
git add api/event/oversized.go api/event/oversized_test.go api/event/event.go api/event/event_test.go api/event/types.go api/server/server.go api/server/server_test.go cmd/exec/run.go cmd/exec/exec.go cmd/websh/websh.go
git rm cmd/exec/oversized.go cmd/exec/oversized_test.go
```

(`api/event/types.go`는 워킹트리의 미커밋 주석 수정을 함께 포함.)

Subject: `refactor(exec): decide oversized at submit time`
Body 요지: oversized 판정이 (command 길이, platform)의 순수 함수인데 cmd 레이어 프리플라이트로 두면 ① 이름→ID resolve가 이중 실행되고 ② 플랫폼 조회가 MFA/토큰갱신 재시도 인프라 밖에 있으며 ③ bool이 6개 시그니처를 관통한다. `SubmitCommand`가 이미 하는 단일 조회(list 응답에 platform 포함)에서 판정하도록 이동—추가 round-trip 0, 재시도 자동 커버, 시그니처 원복. Windows 거부는 제출 전 반환 에러로 유지.

---

## Phase 3 — 테이블 프리뷰 강화 (바운딩 + 제어문자 소독)

### Task 4: `previewForTable` 헬퍼

**Files:**
- Modify: `api/event/event.go:22-23, 62-72` (`newlineFlattener` 주변 + `GetEventList` 투영)
- Test: `api/event/event_test.go` (신규 테스트 추가; 기존 `TestGetEventList_FlattensMultilineForTable`은 그대로 통과해야 함)

**Interfaces:**
- Produces: `func previewForTable(s string, limit int) string` — `GetEventList` 전용 비공개 헬퍼.

- [ ] **Step 1: 실패하는 테스트 작성**

`api/event/event_test.go`에 추가:

```go
func TestPreviewForTable(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"flattens newlines", "a\r\nb\nc", 100, "a b c"},
		{"tab becomes space", "a\tb", 100, "a b"},
		{"strips ansi escape", "ok\x1b[31mred", 100, "ok[31mred"},
		{"strips bel and backspace", "a\x07b\x08c", 100, "abc"},
		{"truncates long input", strings.Repeat("x", 150), 100, strings.Repeat("x", 100) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, previewForTable(tt.in, tt.limit))
		})
	}
}

func TestPreviewForTable_BoundsHugeInput(t *testing.T) {
	// A 1 MiB result must not be fully flattened to render a 100-char cell.
	huge := strings.Repeat("x", 1<<20)
	assert.Equal(t, strings.Repeat("x", 100)+"...", previewForTable(huge, 100))
}
```

- [ ] **Step 2: 실패 확인**

Run: `go test ./api/event/ -run TestPreviewForTable -v`
Expected: FAIL — `undefined: previewForTable`

- [ ] **Step 3: 구현**

`api/event/event.go` — import에 `unicode`, `unicode/utf8` 추가. `newlineFlattener` var 아래에 헬퍼 추가:

```go
// previewForTable renders remote-controlled text as one bounded table cell: it
// caps work on huge inputs before allocating, flattens newlines, and strips
// remaining control runes (e.g. ANSI escapes) so output cannot spoof rows.
func previewForTable(s string, limit int) string {
	if maxBytes := 4 * (limit + 1); len(s) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	s = newlineFlattener.Replace(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	return utils.TruncateString(s, limit)
}
```

(`4*(limit+1)` = 최악(4바이트/룬) 기준 limit+1 룬 확보 → `TruncateString`(룬 기반)이 `...` 접미를 정상 부착. ESC 제거 후 남는 `[31m` 텍스트 잔여물은 무해—터미널 제어가 무력화되는 것이 목적. 제어문자만으로 찬 입력은 `...` 없이 짧게 나올 수 있음—수용.)

`GetEventList` 투영(66-67행)을 교체:

```go
			Command:     previewForTable(event.Line, 100),
			Result:      previewForTable(event.Result, 70),
```

- [ ] **Step 4: 통과 확인**

Run: `go test ./api/event/ -v`
Expected: PASS — 신규 2개 + 기존 `TestGetEventList_FlattensMultilineForTable` 포함 전체

- [ ] **Step 5: 커밋**

```bash
git add api/event/event.go api/event/event_test.go
```

Subject: `fix(event): bound and sanitize event table previews`
Body 요지: `GetEventList`가 원격 제어 텍스트(커맨드/결과)를 전체 flatten 후 100/70자로 자르던 것을, 룬 안전 사전 바운딩 + 개행 평탄화 + 제어 룬 제거(ANSI escape 스푸핑 차단)로 교체. oversized 커맨드(최대 256KB)가 리스트에 등장하는 브랜치라 바운딩이 실질 의미를 가짐.

---

## Phase 4 — 최종 검증

- [ ] **Step 1: 전체 검증 실행**

```bash
go build ./... && go vet ./... && go test -race ./...
golangci-lint run ./...
```

Expected: 빌드·vet·lint 무출력, 테스트 전체 PASS.

- [ ] **Step 2: 잔여 심볼 확인**

```bash
grep -rn "ResolveOversized\|GetServerPlatform\|exceedsInlineLimit\|windowsPlatform" --include="*.go" .
```

Expected: 출력 없음.

- [ ] **Step 3: 결과 보고** — 커밋 3개(Task 1 / Task 2+3 / Task 4)와 검증 출력을 사용자에게 보고. push는 지시가 있을 때만.

---

## Self-review 메모

- Finding 9(복합 단언)·10(testify)은 별도 태스크 없이 Task 1·3의 테스트 재작성에 흡수됨 — 계획상 누락 아님.
- `GetServerIDByName` 1줄 위임은 ponytail 관점에서 허용: 콜러 다수(iam·websh·event 등)의 수정 회피가 목적이고, 제거 시 diff가 오히려 커짐.
- Windows 거부 UX 변화: 기존 전용 메시지+즉시 종료 → `failed to execute command on 'x' server: oversized commands are not supported on Windows servers`(exit 1). 제출 전 차단은 동일(`SubmitCommand`가 POST 전에 반환), 종료 코드 불변.
