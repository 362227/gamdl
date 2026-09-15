package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	puppyready "git.gay/itouakirai/puppyready"
)

type licenseResponse struct {
	Status  int    `json:"status"`
	License string `json:"license"`
	Data    struct {
		License string `json:"license"`
	} `json:"data"`
}

func main() {
	pssh := flag.String("pssh", "", "PlayReady key URI payload")
	adamID := flag.String("adam-id", "", "Apple Adam ID")
	server := flag.String("wrapper-url", "http://127.0.0.1", "combined wrapper URL")
	flag.Parse()
	if *pssh == "" || *adamID == "" {
		fatal(errors.New("-pssh and -adam-id are required"))
	}

	prefix, payload, ok := strings.Cut(*pssh, ",")
	if !ok || prefix == "" || payload == "" {
		fatal(errors.New("invalid PlayReady key URI"))
	}
	parsed, err := puppyready.ParsePSSH(payload)
	if err != nil || len(parsed.WRMHeaders) == 0 {
		if err == nil {
			err = errors.New("PlayReady PSSH has no WRM header")
		}
		fatal(err)
	}

	device, err := puppyready.DefaultDevice()
	if err != nil {
		fatal(fmt.Errorf("initialize PlayReady device: %w", err))
	}
	cdm := puppyready.NewCDM(device)
	session, err := cdm.Open()
	if err != nil {
		fatal(fmt.Errorf("open PlayReady session: %w", err))
	}
	defer cdm.Close(session)

	challenge, err := cdm.GetLicenseChallenge(session, parsed.WRMHeaders[0])
	if err != nil {
		fatal(fmt.Errorf("create PlayReady challenge: %w", err))
	}
	body, err := json.Marshal(map[string]string{
		"challenge": base64.StdEncoding.EncodeToString([]byte(challenge)),
		"uri":       prefix + "," + payload,
		"adamId":    *adamID,
		"drm-type":  "pr",
	})
	if err != nil {
		fatal(err)
	}
	resp, err := http.Post(strings.TrimRight(*server, "/")+"/license", "application/json", strings.NewReader(string(body)))
	if err != nil {
		fatal(fmt.Errorf("request PlayReady license: %w", err))
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("wrapper /license returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody))))
	}
	var license licenseResponse
	if err := json.Unmarshal(responseBody, &license); err != nil {
		fatal(fmt.Errorf("parse wrapper license response: %w", err))
	}
	encoded := license.License
	if encoded == "" {
		encoded = license.Data.License
	}
	if encoded == "" || (license.Status != 0 && license.Status != 200) {
		fatal(fmt.Errorf("wrapper returned no PlayReady license: %s", summarizeResponse(responseBody)))
	}
	licenseXML, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		fatal(fmt.Errorf("decode PlayReady license: %w", err))
	}
	if err := cdm.ParseLicense(session, licenseXML); err != nil {
		fatal(fmt.Errorf("parse PlayReady license: %w", err))
	}
	keys, err := cdm.GetKeys(session)
	if err != nil || len(keys) == 0 || len(keys[0].Key) == 0 {
		fatal(errors.New("PlayReady license returned no content key"))
	}
	fmt.Printf("%x\n", keys[0].Key)
}

func summarizeResponse(body []byte) string {
	var value struct {
		Errors          json.RawMessage `json:"errors"`
		Error           string          `json:"error"`
		Detail          string          `json:"detail"`
		CustomerMessage string          `json:"customerMessage"`
		FailureType     string          `json:"failureType"`
	}
	if json.Unmarshal(body, &value) == nil {
		if value.Error != "" || value.Detail != "" {
			return value.Error + ": " + value.Detail
		}
		if value.CustomerMessage != "" {
			if value.FailureType != "" {
				return value.CustomerMessage + " (Apple error " + value.FailureType + ")"
			}
			return value.CustomerMessage
		}
		if len(value.Errors) > 0 {
			return string(value.Errors)
		}
	}
	return strings.TrimSpace(string(body))
}

func fatal(err error) {
	// stdout is a machine-readable key channel; diagnostics go to stderr.
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
