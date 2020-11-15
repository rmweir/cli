package cmd

import (
	"fmt"
	"github.com/rancher/norman/clientbase"
	client "github.com/rancher/types/client/management/v3"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
)

const (
	kubeconfigpath = ".rancher/.cache/kubeconfig"
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
	filename := fmt.Sprintf("%s-%s-kubeconfig", currentUser, currentCluster)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	kubeconfigFilePath := fmt.Sprintf("%s/%s/%s", home, kubeconfigpath, filename)

	kubeconfig, err := loadKubeconfig(kubeconfigFilePath)
	if err != nil {
		return err
	}

	var isTokenValid bool
	if len(kubeconfig) != 0 {
		tokenID, err := extractKubeconfigTokenID(kubeconfig)
		if err != nil {
			return err
		}
		isTokenValid, err = validateToken(tokenID, c.ManagementClient.Token)
		if err != nil {
			return err
		}
	}

	if len(kubeconfig) == 0 || !isTokenValid {
		cluster, err := getClusterByID(c, c.UserConfig.FocusedCluster())
		if err != nil {
			return err
		}


		config, err := c.ManagementClient.Cluster.ActionGenerateKubeconfig(cluster)
		if err != nil {
			return err
		}
		kubeconfig = []byte(config.Config)
		if err := writeKubeconfig(kubeconfigFilePath, kubeconfig); err != nil {
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

func loadKubeconfig(path string) ([]byte, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return content, err
}

func writeKubeconfig(filePath string, kubeconfig []byte) error {
	err := os.MkdirAll(path.Dir(filePath), 0700)
	if err != nil {
		return err
	}

	logrus.Infof("Saving config to %s", filePath)

	if err := os.Remove(filePath); err != nil {
		return err
	}
	output, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer output.Close()

	_, err = output.Write(kubeconfig)
	return err
}

func extractKubeconfigTokenID(kubeconfig []byte) (string, error) {
	type Token struct {
		Users []struct {
			User struct {
				T string `yaml:"token"`
			} `yaml:"user"`
		} `json:"users,omitempty" yaml:"users,omitempty"`
	}

	currentKubeconfigToken := &Token{}
	a := string(kubeconfig)
	fmt.Println(a)
	err := yaml.Unmarshal(kubeconfig, currentKubeconfigToken)
	if err != nil {
		return "", err
	}
	if len(currentKubeconfigToken.Users) != 1 {
		fmt.Errorf("invalid kubeconfig, expected to contain exactly 1 user")
	}
	parts := strings.Split(currentKubeconfigToken.Users[0].User.T, ":")
	if len(parts) != 2 {
		fmt.Errorf("failed to parse kubeconfig token")
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