package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

func printStatusUsage(out io.Writer) {
	fmt.Fprintln(out, "telos status — report boot state for every service")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  telos status [--json]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Shows two independent views:")
	fmt.Fprintln(out, "  Containers            did each service start, per the container runtime")
	fmt.Fprintln(out, "  Gateway dependencies  what telos-core can actually reach, when it is serving")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "These answer different questions: a container can be up while the gateway")
	fmt.Fprintln(out, "still cannot authenticate to it.")
}

type ContainerStatusRow struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Detail  string `json:"detail"`
}

type GatewayCheck struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Metric interface{} `json:"metric"`
	Detail string      `json:"detail"`
}

type GatewayHealth struct {
	Status string         `json:"status"`
	Checks []GatewayCheck `json:"checks"`
}

type StatusJSONResponse struct {
	Runtime    string               `json:"runtime"`
	Containers []ContainerStatusRow `json:"containers"`
	Gateway    json.RawMessage      `json:"gateway"`
}

func classifyState(raw string, name string) (string, string) {
	if raw == "" {
		return "none", "not created"
	}

	switch {
	case strings.HasPrefix(raw, "Up") || strings.HasPrefix(raw, "up"):
		switch {
		case strings.Contains(raw, "(healthy)"):
			return "ok", "healthy"
		case strings.Contains(raw, "(starting)"):
			return "warn", "starting"
		case strings.Contains(raw, "(unhealthy)"):
			return "bad", "unhealthy"
		default:
			return "ok", "running (no healthcheck)"
		}
	case strings.HasPrefix(raw, "Exited (0)") || strings.HasPrefix(raw, "exited (0)"):
		if isOneshot(name) {
			return "ok", "completed"
		}
		return "bad", "stopped"
	case strings.HasPrefix(raw, "Exited") || strings.HasPrefix(raw, "exited"):
		re := regexp.MustCompile(`[Ee]xited \((\d+)\)`)
		matches := re.FindStringSubmatch(raw)
		if len(matches) > 1 {
			return "bad", fmt.Sprintf("exited (code %s)", matches[1])
		}
		return "bad", "exited"
	case strings.HasPrefix(raw, "Created") || strings.HasPrefix(raw, "created"):
		return "warn", "created, never started"
	case strings.HasPrefix(raw, "Paused") || strings.HasPrefix(raw, "paused"):
		return "warn", "paused"
	default:
		return "warn", raw
	}
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			printStatusUsage(stdout)
			return 0
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "status: unknown option '%s'\n", arg)
				return 2
			}
		}
	}

	runtime, err := detectContainerRuntime()
	if err != nil {
		fmt.Fprintf(stderr, "telos: %v\n", err)
		return 2
	}

	repoRoot := getRepoRoot()
	expected, err := expectedContainers(repoRoot, nil)
	if err != nil {
		fmt.Fprintf(stderr, "telos: %v\n", err)
		return 2
	}

	psMap, _ := getPsOutput(runtime)

	var rows []ContainerStatusRow
	total := 0
	healthy := 0
	problems := 0

	for _, name := range expected {
		total++
		raw := psMap[name]
		cls, label := classifyState(raw, name)
		switch cls {
		case "ok":
			healthy++
		case "bad":
			problems++
		}
		rows = append(rows, ContainerStatusRow{
			Service: name,
			State:   cls,
			Detail:  label,
		})
	}

	healthURL := os.Getenv("TELOS_HEALTH_URL")
	if healthURL == "" {
		healthURL = "http://127.0.0.1:8080/api/v1/health"
	}

	rawGatewayBytes, fetchErr := gatewayHealthFetcher(healthURL)
	var gatewayParsed *GatewayHealth
	if fetchErr == nil && len(rawGatewayBytes) > 0 {
		var gh GatewayHealth
		if json.Unmarshal(rawGatewayBytes, &gh) == nil {
			gatewayParsed = &gh
		}
	}

	if jsonOut {
		resp := StatusJSONResponse{
			Runtime:    runtime,
			Containers: rows,
			Gateway:    json.RawMessage("null"),
		}
		if gatewayParsed != nil && len(rawGatewayBytes) > 0 {
			resp.Gateway = json.RawMessage(rawGatewayBytes)
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(stdout, string(data))

		if healthy == total {
			return 0
		}
		return 1
	}

	color := useColor(stdout)
	fmt.Fprintf(stdout, "%s  %s\n\n", boldText("Telos stack", color), dimText(fmt.Sprintf("(%s)", runtime), color))

	for _, row := range rows {
		label := row.Detail
		if label == "running (no healthcheck)" {
			if color {
				label = "running " + dimText("(no healthcheck)", color)
			}
		}
		fmt.Fprintf(stdout, "  %s  %-22s %s\n", paintIcon(row.State, color), row.Service, label)
	}

	if gatewayParsed != nil {
		fmt.Fprintf(stdout, "\n%s  %s\n\n", boldText("Gateway dependencies", color), dimText(fmt.Sprintf("(%s)", gatewayParsed.Status), color))
		for _, check := range gatewayParsed.Checks {
			checkCls := "warn"
			switch check.Status {
			case "ok":
				checkCls = "ok"
			case "fail":
				checkCls = "bad"
			}
			extra := check.Detail
			if extra == "" && check.Metric != nil {
				extra = fmt.Sprintf("%v", check.Metric)
			}
			fmt.Fprintf(stdout, "  %s  %-22s %s\n", paintIcon(checkCls, color), check.Name, dimText(extra, color))
		}
	} else {
		fmt.Fprintf(stdout, "\n%s  %s\n", boldText("Gateway dependencies", color), dimText("unreachable — telos-core is not serving", color))
	}

	fmt.Fprintf(stdout, "\n%s", boldText(fmt.Sprintf("%d/%d services up", healthy, total), color))
	if problems > 0 {
		fmt.Fprintf(stdout, "%s\n", redText(fmt.Sprintf(", %d with problems", problems), color))
		fmt.Fprintf(stdout, "      %s\n", dimText("Inspect a failure with: telos logs <service>", color))
		return 1
	}
	fmt.Fprintln(stdout, "")

	if healthy == total {
		return 0
	}
	return 1
}
