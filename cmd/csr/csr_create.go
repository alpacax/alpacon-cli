package csr

import (
	"fmt"
	"os/user"
	"path/filepath"
	"strings"

	certApi "github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cert"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	defaultPrivateKeyDir string
	defaultCSRDir        string
)

const (
	infoMessage = "Please specify the paths for the private key and CSR files.\n" +
		"If an existing key is found at the specified path, it will be used.\n" +
		"Otherwise, a new key will be generated.\n" +
		"Note: Root permission may be required for certain paths."
)

var csrFlags struct {
	domains   string
	ips       string
	validDays int
	keyPath   string
	csrPath   string
}

var csrCreateCmd = &cobra.Command{
	Use:   "create [flags]",
	Short: "Create a CSR",
	Long: `
	Generates a new Certificate Signing Request based on provided information,
	which can then be submitted for signing to a certificate authority.
	`,
	Example: `
	alpacon csr create                                   # interactive
	alpacon csr create --domain test-cli.alpacax.lab     # non-interactive
	alpacon csr create -d "a.com,b.com" --valid-days 90
	`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		var signRequest certApi.SignRequest
		var certPath cert.CertificatePath

		nonInteractive := cmd.Flags().Changed("domain") || cmd.Flags().Changed("ip")
		if !nonInteractive && (cmd.Flags().Changed("valid-days") || cmd.Flags().Changed("key") || cmd.Flags().Changed("out")) {
			utils.CliErrorWithExit("--valid-days, --key, and --out require --domain or --ip to be specified.")
		}
		if nonInteractive {
			signRequest.DomainList = []string{}
			signRequest.IpList = []string{}
			if csrFlags.domains != "" {
				signRequest.DomainList = splitAndTrim(csrFlags.domains)
			}
			if csrFlags.ips != "" {
				signRequest.IpList = splitAndTrim(csrFlags.ips)
			}
			if len(signRequest.DomainList) == 0 && len(signRequest.IpList) == 0 {
				utils.CliErrorWithExit("You must provide at least one valid domain or IP address.")
			}
			if csrFlags.validDays <= 0 {
				utils.CliErrorWithExit("--valid-days must be a positive integer.")
			}
			signRequest.ValidDays = csrFlags.validDays
			commonName := firstOf(signRequest.DomainList, signRequest.IpList)
			certPath.PrivateKeyPath = csrFlags.keyPath
			if certPath.PrivateKeyPath == "" {
				certPath.PrivateKeyPath = filepath.Join(defaultPrivateKeyDir, commonName+".key")
			}
			certPath.CSRPath = csrFlags.csrPath
			if certPath.CSRPath == "" {
				certPath.CSRPath = filepath.Join(defaultCSRDir, commonName+".csr")
			}
		} else {
			signRequest, certPath = promptForCert()
		}

		EnsureSecureConnection(alpaconClient)

		response, err := certApi.CreateSignRequest(alpaconClient, signRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to send sign request to server: %s.", err)
		}

		csr, err := cert.CreateCSR(response, certPath)
		if err != nil {
			utils.CliErrorWithExit("Failed to create CSR file: %s.", err)
		}

		err = certApi.SubmitCSR(alpaconClient, csr, response.SubmitURL)
		if err != nil {
			utils.CliErrorWithExit("Failed to submit CSR file to server: %s.", err)
		}

		utils.CliSuccess("CSR created (ID: %s). After approval, run: alpacon csr download-crt %s", response.ID, response.ID)
	},
}

func init() {
	usr, err := user.Current()
	if err != nil {
		utils.CliErrorWithExit("Failed to obtain the current user information: %v", err)
	}

	defaultPrivateKeyDir = filepath.Join(usr.HomeDir, "tmp/private/")
	defaultCSRDir = filepath.Join(usr.HomeDir, "tmp/")

	csrCreateCmd.Flags().StringVarP(&csrFlags.domains, "domain", "d", "", "Comma-separated domain list (e.g., a.com,b.com)")
	csrCreateCmd.Flags().StringVarP(&csrFlags.ips, "ip", "i", "", "Comma-separated IP list (e.g., 192.168.1.1)")
	csrCreateCmd.Flags().IntVar(&csrFlags.validDays, "valid-days", 365, "Certificate validity in days")
	csrCreateCmd.Flags().StringVarP(&csrFlags.keyPath, "key", "k", "", "Path for the private key file")
	csrCreateCmd.Flags().StringVarP(&csrFlags.csrPath, "out", "o", "", "Path for the CSR output file")
}

func promptForCert() (certApi.SignRequest, cert.CertificatePath) {
	var signRequest certApi.SignRequest
	var certPath cert.CertificatePath

	signRequest.DomainList = splitAndTrim(utils.PromptForInput("Domain list (e.g., domain1.com, domain2.com): "))
	signRequest.IpList = splitAndTrim(utils.PromptForInput("IP list (e.g., 192.168.1.1, 10.0.0.1): "))

	if (len(signRequest.DomainList) == 0) && (len(signRequest.IpList) == 0) {
		utils.CliErrorWithExit("You must enter at least a domain list or an IP list.")
	}

	signRequest.ValidDays = utils.PromptForIntInput("Valid days (default: 365): ", 365)

	var commonName string
	if len(signRequest.DomainList) > 0 {
		commonName = signRequest.DomainList[0]
	} else {
		commonName = signRequest.IpList[0]
	}

	defaultKeyPath := fmt.Sprintf("%s/%s.key", defaultPrivateKeyDir, commonName)
	defaultCSRPath := fmt.Sprintf("%s/%s.csr", defaultCSRDir, commonName)

	utils.CliInfo(infoMessage)

	certPath.PrivateKeyPath = utils.PromptForInput(fmt.Sprintf("Path for the Private Key file (default: %s): ", defaultKeyPath))
	if certPath.PrivateKeyPath == "" {
		certPath.PrivateKeyPath = defaultKeyPath
	}

	certPath.CSRPath = utils.PromptForInput(fmt.Sprintf("Path for the CSR file (default: %s): ", defaultCSRPath))
	if certPath.CSRPath == "" {
		certPath.CSRPath = defaultCSRPath
	}

	return signRequest, certPath
}

// EnsureSecureConnection checks if the server uses HTTPS and prompts the user
// to confirm proceeding with an insecure connection if necessary.
func EnsureSecureConnection(client *client.AlpaconClient) {
	isTLS, err := client.IsUsingHTTPS()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}
	if !isTLS {
		utils.CliWarning("The connection to %s might not be secure.", client.BaseURL)

		if !utils.IsInteractiveShell() {
			utils.CliErrorWithExit("Cannot confirm insecure connection in a non-interactive environment. Configure the Alpacon API endpoint to use HTTPS.")
		}

		proceed := utils.PromptForBool("Do you want to proceed with the CSR submission?")
		if !proceed {
			utils.CliErrorWithExit("CSR submission cancelled by user.")
		}

	}
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func firstOf(a, b []string) string {
	if len(a) > 0 {
		return a[0]
	}
	if len(b) > 0 {
		return b[0]
	}
	return ""
}
