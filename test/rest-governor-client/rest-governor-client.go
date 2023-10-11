package main

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

var (
	rootCmd = &cobra.Command{
		Use:   "rest-governor-client",
		Short: "Simple REST client acting as a governor for LXD",
		Run:   run,
	}

	governorDeploymentKeyPath  string
	governorDeploymentCertPath string
	targetDeployment           string
	targetProject              string
	serverAddr                 string
)

func init() {
	rootCmd.Flags().StringVarP(&governorDeploymentCertPath, "governor-deployment-certificate", "", "", "Path to governor deployment certificate")
	rootCmd.Flags().StringVarP(&governorDeploymentKeyPath, "governor-deployment-key", "", "", "Path to the governor deployment key")
	rootCmd.Flags().StringVarP(&targetDeployment, "deployment", "d", "", "Name of the deployment to manage")
	rootCmd.Flags().StringVarP(&targetProject, "project", "p", "", "Name of the project to manage")
	rootCmd.Flags().StringVarP(&serverAddr, "server-addr", "a", "https://<lxd-server-ip>:8443", "LXD server address")
}

func generatedInstanceName(instanceType api.InstanceType, deploymentName string, deploymentShapeName string, deployedInstances []string) string {
	var prefix string
	if instanceType == api.InstanceTypeContainer {
		prefix = "c"
	} else {
		prefix = "v"
	}

	c := len(deployedInstances)
	if c == 0 {
		return fmt.Sprintf("%s-%s-%s%d", deploymentName, deploymentShapeName, prefix, c)
	}

	// Get the highest instance identifier
	highest := 0
	var number int
	for _, instURL := range deployedInstances {
		instName := path.Base(instURL)
		fmt.Sscanf(instName, "%s%d", prefix, &number)
		if number > highest {
			highest = number + 1
		}
	}

	if highest > c {
		c = highest
	}

	return fmt.Sprintf("%s-%s-%s%d", deploymentName, deploymentShapeName, prefix, c)
}

func shapeManager(c lxd.InstanceServer, wg *sync.WaitGroup, stopChan <-chan struct{}, shape api.DeploymentShape) {
	defer wg.Done()

	var instanceCount int
	shapeScaleMaximum := shape.ScalingMaximum

	// Get the number of instances of the shape
	//
	// If the number of instances is less than the minimum,
	// first, spawn new instances until the minimum is reached,
	// and continue to add instances until the maximum is reached.
	// This is just a simple example (we don't react to LXD infrastructure metrics.
	// In a real-world scenario, we don't want to spawn instances to reach the maximum unless needed)
	//
	// If the number of instances is greater than the maximum,
	// delete instances until the maximum is reached.

	timerDuration := 4 * time.Second

	for {
		select {
		default:
			deployedInstances, err := c.GetInstanceNamesInDeploymentShape(targetDeployment, shape.Name)
			if err != nil {
				log.Printf("Failed to get instances in shape %q: %v", shape.Name, err)
				time.Sleep(timerDuration)
				continue
			}

			instanceCount = len(deployedInstances)
			if instanceCount < shapeScaleMaximum {
				log.Printf("Shape %q has %d instances, scaling up to %d", shape.Name, instanceCount, shapeScaleMaximum)
				// For the sake of this example, we just spawn one instance at a time.
				// In a real-world scenario, we'd maybe want to spawn more instances at once.
				instanceName := generatedInstanceName(shape.InstanceTemplate.Type, targetDeployment, shape.Name, deployedInstances)
				rop, err := c.AddInstanceToDeploymentShape(targetDeployment, api.DeploymentInstancesPost{
					ShapeName:    shape.Name,
					InstanceName: instanceName,
				})
				if err != nil {
					log.Printf("Failed to create instance in shape %q: %v", shape.Name, err)
					time.Sleep(timerDuration)
					continue
				}

				err = rop.Wait()
				if err != nil {
					log.Printf("Failed to wait for operation: %v", err)
					time.Sleep(timerDuration)
					continue
				}

				op, err := c.UpdateDeploymentInstanceState(targetDeployment, shape.Name, instanceName, api.InstanceStatePut{
					Action:  "start",
					Timeout: -1,
				}, "")
				if err != nil {
					log.Printf("Failed to start instance %q in shape %q: %v", instanceName, shape.Name, err)
					time.Sleep(timerDuration)
					continue
				}

				err = op.Wait()
				if err != nil {
					log.Printf("Failed to wait for operation: %v", err)
					time.Sleep(timerDuration)
					continue
				}

				log.Printf("Instance %q launched in shape %q", instanceName, shape.Name)
				continue
			} else if instanceCount > shapeScaleMaximum {
				log.Printf("Shape %q has %d instances, scaling down to %d", shape.Name, instanceCount, shapeScaleMaximum)
				// For the sake of this example, we just delete one instance at a time.
				instanceToDelete := deployedInstances[0]
				op, err := c.DeleteInstanceInDeploymentShape(targetDeployment, shape.Name, instanceToDelete)
				if err != nil {
					log.Printf("Failed to delete instance %q in shape %q: %v", instanceToDelete, shape.Name, err)
					time.Sleep(timerDuration)
					continue
				}

				err = op.Wait()
				if err != nil {
					log.Printf("Failed to wait for operation: %v", err)
					time.Sleep(timerDuration)
					continue
				}

				log.Printf("Instance %q deleted in shape %q", instanceToDelete, shape.Name)
				continue
			} else {
				log.Printf("Shape %q is stable with %q deployed instances", shape.Name, deployedInstances)
				return
			}

		case <-stopChan:
			fmt.Printf("Gracefully stopping shape manager for %q\n", shape.Name)
			return
		}
	}
}

func encodeKeyPair(keyFilePath string, certFilePath string) (key string, cert string, err error) {
	keyPair, err := tls.LoadX509KeyPair(certFilePath, keyFilePath)
	if err != nil {
		return "", "", fmt.Errorf("Error loading governor certificate and key: %v", err)
	}

	var privateKeyBytes []byte
	switch key := keyPair.PrivateKey.(type) {
	case *rsa.PrivateKey:
		privateKeyBytes = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})
	case *ecdsa.PrivateKey:
		privateKeyBytes, err = x509.MarshalECPrivateKey(key)
		if err != nil {
			return "", "", fmt.Errorf("Failed to marshal ECDSA private key: %v", err)
		}

		privateKeyBytes = pem.EncodeToMemory(&pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: privateKeyBytes,
		})
	default:
		return "", "", fmt.Errorf("Unknown private key type")
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: keyPair.Certificate[0],
	})

	key = string(privateKeyBytes)
	cert = string(certPEM)
	err = nil
	return
}

func run(cmd *cobra.Command, args []string) {
	if governorDeploymentCertPath == "" || governorDeploymentKeyPath == "" {
		log.Fatal("Missing governor certificate or key")
	}

	deploymentKey, deploymentCert, err := encodeKeyPair(governorDeploymentKeyPath, governorDeploymentCertPath)
	if err != nil {
		log.Fatalf("Failed to encode governor deployment key pair: %v", err)
	}

	connectionArgs := &lxd.ConnectionArgs{
		TLSClientCert:      deploymentCert,
		TLSClientKey:       deploymentKey,
		InsecureSkipVerify: true,
	}

	conn, err := lxd.ConnectLXD(serverAddr, connectionArgs)
	if err != nil {
		log.Fatalf("Failed to connect to LXD: %v", err)
	}

	if targetProject != "" {
		conn = conn.UseProject(targetProject)
	}

	defer conn.Disconnect()

	// Get the shapes of the target deployment
	// and spawn a managing goroutine for each of them.
	var wg sync.WaitGroup
	stopChan := make(chan struct{})
	deploymentShapes, err := conn.GetDeploymentShapes(targetDeployment)
	if err != nil {
		log.Fatalf("Failed to get deployment shapes: %v", err)
	}

	wg.Add(len(deploymentShapes))
	for _, shape := range deploymentShapes {
		go shapeManager(conn, &wg, stopChan, shape)
	}

	// Setup signal handling
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signals
		close(stopChan)
		log.Print("Received signal, shutting down...")
	}()

	// In this example, each goroutine is infinite.
	// So we'll wait forever until the user kills the process.
	wg.Wait()
	log.Print("All shape managers stopped, exiting...")
}

func main() {
	log.SetOutput(os.Stdout)

	err := rootCmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}
