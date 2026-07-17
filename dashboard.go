package skillmanager

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed dashboard/dist
var dashboardAssets embed.FS

type DashboardSkill struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Note        string   `json:"note,omitempty"`
	Producer    string   `json:"producer,omitempty"`
	Agents      []string `json:"agents"`
	Update      string   `json:"update"`
}

type DashboardProducer struct {
	ID          string   `json:"id"`
	Root        string   `json:"root"`
	RootLabel   string   `json:"rootLabel"`
	Note        string   `json:"note,omitempty"`
	BuildArgv   []string `json:"buildArgv"`
	SkillCount  int      `json:"skillCount"`
	Status      string   `json:"status"`
	StatusLabel string   `json:"statusLabel"`
}

type DashboardAgent struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Short      string `json:"short"`
	SkillCount int    `json:"skillCount"`
	Synced     bool   `json:"synced"`
}

type DashboardState struct {
	Repo      string              `json:"repo"`
	RepoLabel string              `json:"repoLabel"`
	Head      string              `json:"head"`
	Dirty     bool                `json:"dirty"`
	Skills    []DashboardSkill    `json:"skills"`
	Producers []DashboardProducer `json:"producers"`
	Agents    []DashboardAgent    `json:"agents"`
}

type grantRequest struct {
	Consumer string `json:"consumer"`
	Enabled  bool   `json:"enabled"`
}
type producerRequest struct {
	ID     string `json:"id"`
	Root   string `json:"root"`
	Note   string `json:"note"`
	Build  string `json:"build"`
	Output string `json:"output"`
}

func RunDashboard(repo, address string) error {
	return serveDashboard(repo, address, nil)
}

func OpenDashboard(repo, address string) error {
	root, err := repositoryRoot(repo)
	if err != nil {
		return err
	}
	url := "http://" + address
	if dashboardServesRepo(url, root) {
		return openURL(url)
	}
	return serveDashboard(root, address, openURL)
}

func serveDashboard(repo, address string, ready func(string) error) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dashboard listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("dashboard mutation API must listen on loopback, got %q", host)
	}
	root, err := repositoryRoot(repo)
	if err != nil {
		return err
	}
	assets, err := fs.Sub(dashboardAssets, "dashboard/dist")
	if err != nil {
		return err
	}
	server := &dashboardServer{repo: root, assets: http.FileServer(http.FS(assets))}
	httpServer := &http.Server{Addr: address, Handler: server}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	url := "http://" + listener.Addr().String()
	fmt.Fprintf(os.Stdout, "sm dashboard: %s\n", url)
	if ready != nil {
		if err := ready(url); err != nil {
			_ = listener.Close()
			return err
		}
	}
	return httpServer.Serve(listener)
}

func dashboardServesRepo(url, repo string) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Get(url + "/api/state")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var state DashboardState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		return false
	}
	return filepath.Clean(state.Repo) == filepath.Clean(repo)
}

type dashboardServer struct {
	repo     string
	assets   http.Handler
	mutation sync.RWMutex
}

func (server *dashboardServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/api/") {
		server.serveAPI(writer, request)
		return
	}
	if request.URL.Path != "/" {
		if _, err := fs.Stat(dashboardAssets, "dashboard/dist"+request.URL.Path); err != nil {
			request.URL.Path = "/"
		}
	}
	server.assets.ServeHTTP(writer, request)
}

func (server *dashboardServer) serveAPI(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		server.mutation.RLock()
		defer server.mutation.RUnlock()
	} else {
		server.mutation.Lock()
		defer server.mutation.Unlock()
	}
	writer.Header().Set("Content-Type", "application/json")
	var result any
	var err error
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/state":
		result, err = dashboardState(server.repo)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/skills/") && strings.HasSuffix(request.URL.Path, "/files"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/skills/"), "/files")
		result, err = listDashboardSkillFiles(server.repo, id)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/skills/") && strings.HasSuffix(request.URL.Path, "/file"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/skills/"), "/file")
		result, err = readDashboardSkillFile(server.repo, id, request.URL.Query().Get("path"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/api/skills/") && strings.HasSuffix(request.URL.Path, "/grants"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/skills/"), "/grants")
		var input grantRequest
		err = decodeRequest(request, &input)
		if err == nil {
			err = setGrant(server.repo, id, input)
		}
		if err == nil {
			result, err = dashboardState(server.repo)
		}
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/api/producers/") && strings.HasSuffix(request.URL.Path, "/update"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/producers/"), "/update")
		_, err = UpdateProducers(server.repo, []string{id}, os.Stdout, os.Stderr)
		if err == nil {
			err = commitSSOT(server.repo, "Update producer "+id)
		}
		if err == nil {
			err = syncAllConsumers(server.repo)
		}
		if err == nil {
			result, err = dashboardState(server.repo)
		}
	case request.Method == http.MethodPost && request.URL.Path == "/api/producers":
		var input producerRequest
		err = decodeRequest(request, &input)
		if err == nil {
			err = addProducer(server.repo, input)
		}
		if err == nil {
			result, err = dashboardState(server.repo)
		}
	default:
		writer.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(writer).Encode(result)
}

func decodeRequest(request *http.Request, value any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func dashboardState(repo string) (DashboardState, error) {
	root, err := repositoryRoot(repo)
	if err != nil {
		return DashboardState{}, err
	}
	head, err := resolveCommit(root, "HEAD")
	if err != nil {
		return DashboardState{}, err
	}
	status, err := runGit(root, "status", "--porcelain")
	if err != nil {
		return DashboardState{}, err
	}
	state := DashboardState{Repo: root, RepoLabel: displayPath(root), Head: head[:min(8, len(head))], Dirty: strings.TrimSpace(status) != "", Skills: []DashboardSkill{}, Producers: []DashboardProducer{}, Agents: []DashboardAgent{}}
	producers, err := loadProducers(root)
	if err != nil {
		return DashboardState{}, err
	}
	owner := map[string]string{}
	notes := map[string]string{}
	for _, producer := range producers {
		for _, skill := range producer.Skills {
			owner[skill] = producer.ID
			notes[skill] = producer.Note
		}
	}
	updates := map[string]string{}
	producerFailureLabels := map[string]string{}
	report, scanErr := ScanProducers(root, nil)
	if scanErr != nil {
		for _, producer := range producers {
			producerFailureLabels[producer.ID] = "扫描失败"
			for _, skill := range producer.Skills {
				updates[skill] = string(ArtifactInvalid)
			}
		}
	} else {
		for _, scan := range report.Producers {
			if scan.Error != "" {
				producerFailureLabels[scan.Producer.ID] = "来源不可用"
				for _, skill := range scan.Producer.Skills {
					updates[skill] = string(ArtifactInvalid)
				}
				continue
			}
			for _, artifact := range scan.Artifacts {
				updates[artifact.SkillID] = string(artifact.State)
			}
		}
	}
	consumers, err := readConsumers(root)
	if err != nil {
		return DashboardState{}, err
	}
	grants := map[string][]string{}
	for id, consumer := range consumers {
		for _, skill := range consumer.Skills {
			grants[skill] = append(grants[skill], id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return DashboardState{}, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, "skills", entry.Name())
		if err := validateCanonicalSkill(path); err != nil {
			return DashboardState{}, fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		metadata, err := readSkillMetadata(path)
		if err != nil {
			return DashboardState{}, fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		if metadata.Name != entry.Name() {
			return DashboardState{}, fmt.Errorf("skill directory %q does not match frontmatter name %q", entry.Name(), metadata.Name)
		}
		agents := grants[entry.Name()]
		if agents == nil {
			agents = []string{}
		}
		sort.Strings(agents)
		update := updates[entry.Name()]
		if update == "" || update == string(ArtifactUnchanged) {
			update = "current"
		}
		if update == string(ArtifactInvalid) || update == string(ArtifactConflict) {
			update = "error"
		}
		state.Skills = append(state.Skills, DashboardSkill{ID: entry.Name(), Description: metadata.Description, Note: notes[entry.Name()], Producer: owner[entry.Name()], Agents: agents, Update: update})
	}
	sort.Slice(state.Skills, func(i, j int) bool { return state.Skills[i].ID < state.Skills[j].ID })
	for _, producer := range producers {
		status, label := "current", "已是最新"
		if failureLabel := producerFailureLabels[producer.ID]; failureLabel != "" {
			status, label = "error", failureLabel
		}
		for _, skill := range producer.Skills {
			if producerFailureLabels[producer.ID] != "" {
				continue
			}
			if updates[skill] == string(ArtifactUpdated) || updates[skill] == string(ArtifactNew) {
				status, label = "updated", "有新产物"
			}
			if updates[skill] == string(ArtifactInvalid) || updates[skill] == string(ArtifactConflict) {
				status, label = "error", "产物有问题"
			}
		}
		state.Producers = append(state.Producers, DashboardProducer{ID: producer.ID, Root: producer.Root, RootLabel: displayPath(producer.Root), Note: producer.Note, BuildArgv: append([]string(nil), producer.Build.Argv...), SkillCount: len(producer.Skills), Status: status, StatusLabel: label})
	}
	for id, consumer := range consumers {
		name, short := agentIdentity(consumer.Adapter)
		state.Agents = append(state.Agents, DashboardAgent{ID: id, Name: name, Short: short, SkillCount: len(consumer.Skills), Synced: consumerSynced(root, head, id, consumer, state.Dirty)})
	}
	rank := map[string]int{"codex.global": 0, "claude.global": 1, "pi.global": 2}
	sort.Slice(state.Agents, func(i, j int) bool { return rank[state.Agents[i].ID] < rank[state.Agents[j].ID] })
	return state, nil
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	clean := filepath.Clean(path)
	home = filepath.Clean(home)
	if clean == home {
		return "~"
	}
	if strings.HasPrefix(clean, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(clean, home)
	}
	return path
}

func readConsumers(repo string) (map[string]Consumer, error) {
	entries, err := os.ReadDir(filepath.Join(repo, "consumers"))
	if err != nil {
		return nil, err
	}
	result := map[string]Consumer{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(repo, "consumers", entry.Name()))
		if err != nil {
			return nil, err
		}
		var consumer Consumer
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&consumer); err != nil {
			return nil, err
		}
		if err := validateConsumer(consumer); err != nil {
			return nil, err
		}
		result[id] = consumer
	}
	return result, nil
}

func setGrant(repo, skill string, input grantRequest) error {
	if err := validateID(skill); err != nil {
		return err
	}
	if err := validateID(input.Consumer); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", skill, "SKILL.md")); err != nil {
		return fmt.Errorf("unknown skill %q", skill)
	}
	consumers, err := readConsumers(repo)
	if err != nil {
		return err
	}
	consumer, ok := consumers[input.Consumer]
	if !ok {
		return fmt.Errorf("unknown consumer %q", input.Consumer)
	}
	set := map[string]bool{}
	for _, id := range consumer.Skills {
		set[id] = true
	}
	if input.Enabled {
		set[skill] = true
	} else {
		delete(set, skill)
	}
	consumer.Skills = consumer.Skills[:0]
	for id := range set {
		consumer.Skills = append(consumer.Skills, id)
	}
	sort.Strings(consumer.Skills)
	if err := writeJSONAtomic(filepath.Join(repo, "consumers", input.Consumer+".json"), consumer); err != nil {
		return err
	}
	if err := commitSSOT(repo, fmt.Sprintf("Set %s access to %s", input.Consumer, skill)); err != nil {
		return err
	}
	return syncConsumer(repo, input.Consumer, consumer)
}

func syncConsumer(repo, id string, consumer Consumer) error {
	if _, err := Build(repo, "HEAD", id, ""); err != nil {
		return err
	}
	if consumer.Adapter == "codex" {
		if _, err := Apply(repo, "HEAD", id, ""); err != nil {
			return err
		}
		_, _, err := AgentCommand(repo, "HEAD", id, "", nil)
		return err
	}
	if consumer.Adapter == "directory" {
		if _, err := Apply(repo, "HEAD", id, ""); err != nil {
			return err
		}
		_, err := Verify(repo, "HEAD", id, "")
		return err
	}
	return nil
}
func syncAllConsumers(repo string) error {
	consumers, err := readConsumers(repo)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(consumers))
	for id := range consumers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := syncConsumer(repo, id, consumers[id]); err != nil {
			return fmt.Errorf("sync %s: %w", id, err)
		}
	}
	return nil
}

func addProducer(repo string, input producerRequest) error {
	if err := validateID(input.ID); err != nil {
		return err
	}
	root, err := expandHome(input.Root)
	if err != nil {
		return err
	}
	argv := strings.Fields(input.Build)
	if len(argv) == 0 {
		return fmt.Errorf("生成方式不能为空")
	}
	output := input.Output
	if output == "" {
		return fmt.Errorf("产物位置不能为空")
	}
	ids, err := discoverSkillIDs(root, output)
	if err != nil {
		return err
	}
	producer := Producer{ID: input.ID, Root: root, Note: strings.TrimSpace(input.Note), Build: ProducerBuild{Argv: argv}, Outputs: []ProducerOutput{{Path: output}}, Skills: ids}
	if err := validateProducer(producer); err != nil {
		return err
	}
	existing, err := loadProducers(repo)
	if err != nil {
		return err
	}
	for _, item := range existing {
		for _, owned := range item.Skills {
			for _, id := range ids {
				if owned == id {
					return fmt.Errorf("skill %q 已由 %s 管理", id, item.ID)
				}
			}
		}
	}
	if err := writeProducer(repo, producer); err != nil {
		return err
	}
	return commitSSOT(repo, "Add producer "+input.ID)
}

func discoverSkillIDs(root, output string) ([]string, error) {
	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "SKILL.md" {
			metadata, err := readSkillMetadata(filepath.Dir(name))
			if err != nil {
				return err
			}
			seen[metadata.Name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, fmt.Errorf("产物位置没有发现技能")
	}
	return ids, nil
}
func agentIdentity(adapter string) (string, string) {
	switch adapter {
	case "codex":
		return "Codex", "CX"
	case "claude":
		return "Claude", "CL"
	case "pi":
		return "Pi", "PI"
	default:
		return adapter, "AG"
	}
}
func consumerSynced(repo, head, id string, consumer Consumer, dirty bool) bool {
	if dirty {
		return false
	}
	if consumer.Adapter != "codex" && consumer.Adapter != "directory" {
		return true
	}
	target, err := expandHome(consumer.Target)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(resolved, markerName))
	if err != nil {
		return false
	}
	var marker Marker
	if json.Unmarshal(data, &marker) != nil {
		return false
	}
	return marker.Commit == head && marker.Consumer == id
}
