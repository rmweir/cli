package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/urfave/cli"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/norman/clientbase"
	client "github.com/rancher/types/client/management/v3"
)

const (
	kubeConfigNameFormat = "%s-%s-kubeconfig"
)

var (
	kubeConfigPath = os.ExpandEnv("${HOME}/.rancher/.cache/.kubeconfig")
)

func KubectlCommand() cli.Command {
	return cli.Command{
		Name:            "kubectl",
		Usage:           "Run kubectl commands",
		Description:     "Use the current cluster context to run kubectl commands in the cluster",
		Action:          runKubectl,
		SkipFlagParsing: true,
	}
}

func runKubectl(ctx *cli.Context) error {
	args := ctx.Args()
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		return cli.ShowCommandHelp(ctx, "kubectl")
	}

	path, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl is required to be set in your path to use this "+
			"command. See https://kubernetes.io/docs/tasks/tools/install-kubectl/ "+
			"for more info. Error: %s", err.Error())
	}

	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	currentRancherServer := config.FocusedServer()
	if currentRancherServer == nil {
		return fmt.Errorf("no focused server")
	}

	currentToken := currentRancherServer.AccessKey
	t, err := c.ManagementClient.Token.ByID(currentToken)
	if err != nil {
		return err
	}

	currentUser := t.UserID
	currentCluster := currentRancherServer.FocusedCluster()
	filename := fmt.Sprintf(kubeConfigNameFormat, currentUser, currentCluster)
	kubeconfigFilePath := strings.Join([]string{kubeConfigPath, filename}, "/")

	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	var isTokenValid bool
	if kubeconfig != nil {
		tokenID, err := extractKubeconfigTokenID(*kubeconfig)
		if err != nil {
			return err
		}
		isTokenValid, err = validateToken(tokenID, c.ManagementClient.Token)
		if err != nil {
			return err
		}
	}

	if kubeconfig == nil || !isTokenValid {
		cluster, err := getClusterByID(c, c.UserConfig.FocusedCluster())
		if err != nil {
			return err
		}

		config, err := c.ManagementClient.Cluster.ActionGenerateKubeconfig(cluster)
		if err != nil {
			return err
		}
		if err := writeKubeconfig(kubeconfigFilePath, []byte(config.Config)); err != nil {
			return err
		}
	}

	cmd := exec.Command(path, ctx.Args()...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigFilePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

func writeKubeconfig(filePath string, kubeconfig []byte) error {
	config, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return err
	}
	return clientcmd.WriteToFile(*config, filePath)
}

func extractKubeconfigTokenID(kubeconfig api.Config) (string, error) {
	if len(kubeconfig.AuthInfos) != 1 {
		return "", fmt.Errorf("invalid kubeconfig, expected to contain exactly 1 user")
	}
	var parts []string
	for _, val := range kubeconfig.AuthInfos {
		parts = strings.Split(val.Token, ":")
		if len(parts) != 2 {
			return "", fmt.Errorf("failed to parse kubeconfig token")
		}
	}

	return parts[0], nil
}

func validateToken(tokenID string, tokenClient client.TokenOperations) (bool, error) {
	token, err := tokenClient.ByID(tokenID)
	if err != nil {
		if !clientbase.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	return !token.Expired, nil
}
