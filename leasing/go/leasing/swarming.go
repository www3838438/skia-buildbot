/*
	Used by the Leasing Server to interact with swarming.
*/
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os/user"
	"path"
	"strings"

	swarming_api "go.chromium.org/luci/common/api/swarming/swarming/v1"

	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/isolate"
	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/util"
)

type SwarmingInstanceClients struct {
	SwarmingServer string
	SwarmingClient *swarming.ApiClient

	IsolateServer string
	IsolateClient **isolate.Client
}

var (
	isolateClientPublic  *isolate.Client
	isolateClientPrivate *isolate.Client

	swarmingClientPublic  swarming.ApiClient
	swarmingClientPrivate swarming.ApiClient

	PublicSwarming *SwarmingInstanceClients = &SwarmingInstanceClients{
		SwarmingServer: swarming.SWARMING_SERVER,
		IsolateServer:  isolate.ISOLATE_SERVER_URL,
		SwarmingClient: &swarmingClientPublic,
		IsolateClient:  &isolateClientPublic,
	}

	InternalSwarming *SwarmingInstanceClients = &SwarmingInstanceClients{
		SwarmingServer: swarming.SWARMING_SERVER_PRIVATE,
		IsolateServer:  isolate.ISOLATE_SERVER_URL_PRIVATE,
		SwarmingClient: &swarmingClientPrivate,
		IsolateClient:  &isolateClientPrivate,
	}

	PoolsToSwarmingInstance = map[string]*SwarmingInstanceClients{
		"Skia":             PublicSwarming,
		"SkiaCT":           PublicSwarming,
		"SkiaInternal":     InternalSwarming,
		"CT":               InternalSwarming,
		"CTAndroidBuilder": InternalSwarming,
		"CTLinuxBuilder":   InternalSwarming,
	}

	isolateServerPath string
)

func SwarmingInit() error {
	// Public Isolate client.
	var err error
	isolateClientPublic, err = isolate.NewClient(*workdir, isolate.ISOLATE_SERVER_URL)
	if err != nil {
		return fmt.Errorf("Failed to create public isolate client: %s", err)
	}
	// Private Isolate client.
	isolateClientPrivate, err = isolate.NewClient(*workdir, isolate.ISOLATE_SERVER_URL_PRIVATE)
	if err != nil {
		return fmt.Errorf("Failed to create private isolate client: %s", err)
	}

	// Authenticated HTTP client.
	oauthCacheFile := path.Join(*workdir, "google_storage_token.data")
	httpClient, err := auth.NewClient(*local, oauthCacheFile, swarming.AUTH_SCOPE)
	if err != nil {
		return fmt.Errorf("Failed to create authenticated HTTP client: %s", err)
	}
	// Public Swarming API client.
	swarmingClientPublic, err = swarming.NewApiClient(httpClient, swarming.SWARMING_SERVER)
	if err != nil {
		return fmt.Errorf("Failed to create public swarming client: %s", err)
	}
	// Private Swarming API client.
	swarmingClientPrivate, err = swarming.NewApiClient(httpClient, swarming.SWARMING_SERVER_PRIVATE)
	if err != nil {
		return fmt.Errorf("Failed to create private swarming client: %s", err)
	}

	// Set path to the isolateserver.py script.
	usr, err := user.Current()
	if err != nil {
		return fmt.Errorf("Failed to find the current user: %s", err)
	}
	isolateServerPath = path.Join(usr.HomeDir, "client-py", "isolateserver.py")

	return nil
}

func GetSwarmingInstance(pool string) *SwarmingInstanceClients {
	return PoolsToSwarmingInstance[pool]
}

func GetSwarmingClient(pool string) *swarming.ApiClient {
	return GetSwarmingInstance(pool).SwarmingClient
}

func GetIsolateClient(pool string) **isolate.Client {
	return GetSwarmingInstance(pool).IsolateClient
}

type PoolDetails struct {
	OsTypes     map[string]int
	DeviceTypes map[string]int
}

func GetPoolDetails(pool string) (*PoolDetails, error) {
	swarmingClient := *GetSwarmingClient(pool)
	bots, err := swarmingClient.ListBotsForPool(pool)
	if err != nil {
		return nil, fmt.Errorf("Could not list bots in pool: %s", err)
	}
	osTypes := map[string]int{}
	deviceTypes := map[string]int{}
	for _, bot := range bots {
		if bot.IsDead || bot.Quarantined {
			// Do not include dead/quarantined bots in the counts below.
			continue
		}
		for _, d := range bot.Dimensions {
			if d.Key == "os" {
				val := ""
				// Use the longest string from the os values because that is what the swarming UI
				// does and it works in all cases we have (atleast as of 11/1/17).
				for _, v := range d.Value {
					if len(v) > len(val) {
						val = v
					}
				}
				osTypes[val]++
			}
			if d.Key == "device_type" {
				// There should only be one value for device type.
				val := d.Value[0]
				deviceTypes[val]++
			}
		}
	}
	return &PoolDetails{
		OsTypes:     osTypes,
		DeviceTypes: deviceTypes,
	}, nil
}

type IsolateDetails struct {
	Command     []string `json:"command"`
	RelativeCwd string   `json:"relative_cwd"`
	IsolateDep  string
	CipdInput   *swarming_api.SwarmingRpcsCipdInput
}

func GetIsolateDetails(properties *swarming_api.SwarmingRpcsTaskProperties) (*IsolateDetails, error) {
	details := &IsolateDetails{}
	inputsRef := properties.InputsRef

	f, err := ioutil.TempFile(*workdir, inputsRef.Isolated+"_")
	if err != nil {
		return details, fmt.Errorf("Could not create tmp file in %s: %s", *workdir, err)
	}
	defer util.Remove(f.Name())
	cmd := []string{
		isolateServerPath, "download",
		"-I", inputsRef.Isolatedserver,
		"--namespace", inputsRef.Namespace,
		"-f", inputsRef.Isolated, path.Base(f.Name()),
		"-t", *workdir,
	}
	output, err := exec.RunCwd(*workdir, cmd...)
	if err != nil {
		return details, fmt.Errorf("Failed to run cmd %s: %s", cmd, err)
	}

	if err := json.NewDecoder(f).Decode(&details); err != nil {
		return details, fmt.Errorf("Could not decode %s: %s", output, err)
	}
	details.IsolateDep = inputsRef.Isolated
	details.CipdInput = properties.CipdInput
	// Append extra arguments to the command.
	details.Command = append(details.Command, properties.ExtraArgs...)

	return details, nil
}

func GetIsolateHash(pool, osType, isolateDep string) (string, error) {
	isolateClient := *GetIsolateClient(pool)
	isolateTask := &isolate.Task{
		BaseDir:     path.Join(*resourcesDir, "isolates"),
		Blacklist:   []string{},
		IsolateFile: path.Join(*resourcesDir, "isolates", "leasing.isolate"),
		OsType:      osType,
	}
	if isolateDep != "" {
		isolateTask.Deps = []string{isolateDep}
	}
	isolateTasks := []*isolate.Task{isolateTask}
	hashes, err := isolateClient.IsolateTasks(isolateTasks)
	if err != nil {
		return "", fmt.Errorf("Could not isolate leasing task: %s", err)
	}
	if len(hashes) != 1 {
		return "", fmt.Errorf("IsolateTasks returned incorrect number of hashes %d (expected 1)", len(hashes))
	}
	return hashes[0], nil
}

func GetSwarmingTask(pool, taskId string) (*swarming_api.SwarmingRpcsTaskResult, error) {
	swarmingClient := *GetSwarmingClient(pool)
	return swarmingClient.GetTask(taskId, false)
}

func GetSwarmingTaskMetadata(pool, taskId string) (*swarming_api.SwarmingRpcsTaskRequestMetadata, error) {
	swarmingClient := *GetSwarmingClient(pool)
	return swarmingClient.GetTaskMetadata(taskId)
}

func TriggerSwarmingTask(pool, requester, datastoreId, osType, deviceType, botId, serverURLstring, isolateHash string, isolateDetails *IsolateDetails) (string, error) {
	dimsMap := map[string]string{
		"pool": pool,
		"os":   osType,
	}
	if deviceType != "" {
		dimsMap["device_type"] = deviceType
	}
	if botId != "" {
		dimsMap["id"] = botId
	}
	dims := make([]*swarming_api.SwarmingRpcsStringPair, 0, len(dimsMap))
	for k, v := range dimsMap {
		dims = append(dims, &swarming_api.SwarmingRpcsStringPair{
			Key:   k,
			Value: v,
		})
	}

	// Arguments that will be passed to leasing.py
	extraArgs := []string{
		"--task-id", datastoreId,
		"--os-type", osType,
		"--leasing-server", serverURL,
		"--debug-command", strings.Join(isolateDetails.Command, " "),
		"--command-relative-dir", isolateDetails.RelativeCwd,
	}
	isolateServer := GetSwarmingInstance(pool).IsolateServer
	expirationSecs := int64(swarming.RECOMMENDED_EXPIRATION.Seconds())
	executionTimeoutSecs := int64(SWARMING_HARD_TIMEOUT.Seconds())
	ioTimeoutSecs := int64(SWARMING_HARD_TIMEOUT.Seconds())
	taskName := fmt.Sprintf("Leased by %s using leasing.skia.org", requester)
	taskRequest := &swarming_api.SwarmingRpcsNewTaskRequest{
		ExpirationSecs: expirationSecs,
		Name:           taskName,
		Priority:       LEASE_TASK_PRIORITY,
		Properties: &swarming_api.SwarmingRpcsTaskProperties{
			CipdInput:            isolateDetails.CipdInput,
			Dimensions:           dims,
			ExecutionTimeoutSecs: executionTimeoutSecs,
			ExtraArgs:            extraArgs,
			InputsRef: &swarming_api.SwarmingRpcsFilesRef{
				Isolated:       isolateHash,
				Isolatedserver: isolateServer,
				Namespace:      isolate.DEFAULT_NAMESPACE,
			},
			IoTimeoutSecs: ioTimeoutSecs,
		},
		ServiceAccount: swarming.GetServiceAccountFromTaskDims(dimsMap),
		User:           "skiabot@google.com",
	}

	swarmingClient := *GetSwarmingClient(pool)
	resp, err := swarmingClient.TriggerTask(taskRequest)
	if err != nil {
		return "", fmt.Errorf("Could not trigger swarming task %s", err)
	}
	return resp.TaskId, nil
}

func GetSwarmingTaskLink(server, taskId string) string {
	return fmt.Sprintf("https://%s/task?id=%s", server, taskId)
}