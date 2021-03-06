package sctp

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcfgScheme "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned/scheme"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift-kni/cnf-features-deploy/functests/utils/client"
	"github.com/openshift-kni/cnf-features-deploy/functests/utils/execute"
	"github.com/openshift-kni/cnf-features-deploy/functests/utils/namespaces"

	"k8s.io/utils/pointer"
)

const mcYaml = "../sctp/sctp_module_mc.yaml"
const hostnameLabel = "kubernetes.io/hostname"
const testNamespace = "sctptest"

var (
	testerImage      string
	sctpNodeSelector string
	hasNonCnfWorkers bool
)

func init() {
	testerImage = os.Getenv("SCTPTEST_IMAGE")
	if testerImage == "" {
		testerImage = "quay.io/fpaoline/sctptester:v1.0"
	}

	sctpNodeSelector = os.Getenv("SCTPTEST_NODE_SELECTOR")
	if sctpNodeSelector == "" {
		sctpNodeSelector = "node-role.kubernetes.io/worker-cnf="
	}

	hasNonCnfWorkers = true
	if os.Getenv("SCTPTEST_HAS_NON_CNF_WORKERS") == "false" {
		hasNonCnfWorkers = false
	}
}

var _ = Describe("sctp", func() {
	execute.BeforeAll(func() {
		err := namespaces.Create(testNamespace, client.Client)
		Expect(err).ToNot(HaveOccurred())
		err = namespaces.Clean(testNamespace, client.Client)
		Expect(err).ToNot(HaveOccurred())
	})
	var _ = Describe("sctp setup", func() {
		var clientNode string
		var serverNode string

		var _ = Describe("Negative - Sctp disabled", func() {
			execute.BeforeAll(func() {
				By("Validate that SCTP present of cluster.")
				checkForSctpReady(client.Client)
			})
			BeforeEach(func() {
				if !hasNonCnfWorkers {
					Skip("Skipping as no non-enabled nodes are available")
				}

				By("Choosing the nodes for the server and the client")
				nodes, err := client.Client.Nodes().List(metav1.ListOptions{
					LabelSelector: "node-role.kubernetes.io/worker,!" + strings.Replace(sctpNodeSelector, "=", "", -1),
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(len(nodes.Items)).To(BeNumerically(">", 0))
				clientNode = nodes.Items[0].ObjectMeta.Labels[hostnameLabel]
				serverNode = nodes.Items[0].ObjectMeta.Labels[hostnameLabel]
				if len(nodes.Items) > 1 {
					serverNode = nodes.Items[1].ObjectMeta.Labels[hostnameLabel]
				}
				createSctpService(client.Client)
			})
			Context("Client Server Connection", func() {
				// OCP-26995
				It("Should NOT connect a client pod to a server pod", func() {
					By("Starting the server")
					serverArgs := []string{"-ip", "0.0.0.0", "-port", "30101", "-server"}
					pod := scptTestPod("scptserver", serverNode, "sctpserver", serverArgs)
					pod.Spec.Containers[0].Ports = []k8sv1.ContainerPort{
						k8sv1.ContainerPort{
							Name:          "sctpport",
							Protocol:      k8sv1.ProtocolSCTP,
							ContainerPort: 30101,
						},
					}
					serverPod, err := client.Client.Pods(testNamespace).Create(pod)
					Expect(err).ToNot(HaveOccurred())
					By("Fetching the server's ip address")
					Eventually(func() k8sv1.PodPhase {
						runningPod, err := client.Client.Pods(testNamespace).Get(serverPod.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return runningPod.Status.Phase
					}, 1*time.Minute, 1*time.Second).Should(Equal(k8sv1.PodFailed))
				})

				AfterEach(func() {
					namespaces.Clean("sctptest", client.Client)
					deleteService(client.Client)
				})
			})
		})
		var _ = Describe("Test Connectivity", func() {
			var activeService *k8sv1.Service
			var runningPod *k8sv1.Pod
			var serverPod *k8sv1.Pod
			var nodes *k8sv1.NodeList
			var err error

			execute.BeforeAll(func() {
				checkForSctpReady(client.Client)
			})
			BeforeEach(func() {
				By("Choosing the nodes for the server and the client")
				nodes, err = client.Client.Nodes().List(metav1.ListOptions{
					LabelSelector: sctpNodeSelector,
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(nodes.Items)).To(BeNumerically(">", 0))
				clientNode = nodes.Items[0].ObjectMeta.Labels[hostnameLabel]
				serverNode = nodes.Items[0].ObjectMeta.Labels[hostnameLabel]
				if len(nodes.Items) > 1 {
					serverNode = nodes.Items[1].ObjectMeta.Labels[hostnameLabel]
				}
				activeService = createSctpService(client.Client)
				By("Starting the server")
				serverArgs := []string{"-ip", "0.0.0.0", "-port", "30101", "-server"}
				pod := scptTestPod("scptserver", serverNode, "sctpserver", serverArgs)
				pod.Spec.Containers[0].Ports = []k8sv1.ContainerPort{
					k8sv1.ContainerPort{
						Name:          "sctpport",
						Protocol:      k8sv1.ProtocolSCTP,
						ContainerPort: 30101,
					},
				}
				serverPod, err = client.Client.Pods(testNamespace).Create(pod)
				Expect(err).ToNot(HaveOccurred())
				By("Fetching the server's ip address")
				Eventually(func() k8sv1.PodPhase {
					runningPod, err = client.Client.Pods(testNamespace).Get(serverPod.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPod.Status.Phase
				}, 3*time.Minute, 1*time.Second).Should(Equal(k8sv1.PodRunning))

			})
			Context("Client Server Connection", func() {
				// OCP-26759
				It("Kernel Module is loaded", func() {
					checkForSctpReady(client.Client)
				})
				// OCP-26760
				It("Should connect a client pod to a server pod", func() {
					testClientServerConnection(client.Client, runningPod.Status.PodIP,
						activeService.Spec.Ports[0].Port, clientNode, serverPod.Name)
				})
				// OCP-26763
				It("Should connect a client pod to a server pod. Feature LatencySensitive Active", func() {
					fg, err := client.Client.FeatureGates().Get("cluster", metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					if fg.Spec.FeatureSet == "LatencySensitive" {
						testClientServerConnection(client.Client, runningPod.Status.PodIP,
							activeService.Spec.Ports[0].Port, clientNode, serverPod.Name)
					} else {
						Skip("Feature LatencySensitive is not ACTIVE")
					}
				})
				// OCP-26761
				It("Should connect a client pod to a server pod via Service ClusterIP", func() {
					testClientServerConnection(client.Client, activeService.Spec.ClusterIP,
						activeService.Spec.Ports[0].Port, clientNode, serverPod.Name)
				})
				// OCP-26762
				It("Should connect a client pod to a server pod via Service NodeIP", func() {
					testClientServerConnection(client.Client, nodes.Items[0].Status.Addresses[0].Address,
						activeService.Spec.Ports[0].Port, clientNode, serverPod.Name)
				})
			})
			AfterEach(func() {
				deleteService(client.Client)
				namespaces.Clean("sctptest", client.Client)
			})
		})
	})
})

func loadMC() *mcfgv1.MachineConfig {
	decode := mcfgScheme.Codecs.UniversalDeserializer().Decode
	mcoyaml, err := ioutil.ReadFile(mcYaml)
	Expect(err).ToNot(HaveOccurred())

	obj, _, err := decode([]byte(mcoyaml), nil, nil)
	Expect(err).ToNot(HaveOccurred())
	mc, ok := obj.(*mcfgv1.MachineConfig)
	Expect(ok).To(Equal(true))
	return mc
}

func checkForSctpReady(cs *client.ClientSet) {
	nodes, err := cs.Nodes().List(metav1.ListOptions{
		LabelSelector: sctpNodeSelector,
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(len(nodes.Items)).To(BeNumerically(">", 0))

	args := []string{`set -x; x="$(checksctp 2>&1)"; echo "$x" ; if [ "$x" = "SCTP supported" ]; then echo "succeeded"; exit 0; else echo "failed"; exit 1; fi`}
	for _, n := range nodes.Items {
		job := jobForNode("checksctp", n.ObjectMeta.Labels[hostnameLabel], "checksctp", []string{"/bin/bash", "-c"}, args)
		cs.Pods(testNamespace).Create(job)
	}

	Eventually(func() bool {
		pods, err := cs.Pods(testNamespace).List(metav1.ListOptions{LabelSelector: "app=checksctp"})
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		for _, p := range pods.Items {
			if p.Status.Phase != k8sv1.PodSucceeded {
				return false
			}
		}
		return true
	}, 3*time.Minute, 10*time.Second).Should(Equal(true))
}

func testClientServerConnection(cs *client.ClientSet, destIP string, port int32, clientNode string, serverPodName string) {
	By("Connecting a client to the server")
	clientArgs := []string{"-ip", destIP, "-port",
		fmt.Sprint(port), "-lport", "30102"}
	clientPod := scptTestPod("sctpclient", clientNode, "sctpclient", clientArgs)
	cs.Pods(testNamespace).Create(clientPod)

	Eventually(func() k8sv1.PodPhase {
		pod, err := cs.Pods(testNamespace).Get(serverPodName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return pod.Status.Phase
	}, 1*time.Minute, 1*time.Second).Should(Equal(k8sv1.PodSucceeded))
}

func createSctpService(cs *client.ClientSet) *k8sv1.Service {
	service := k8sv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sctpservice",
			Namespace: testNamespace,
		},
		Spec: k8sv1.ServiceSpec{
			Selector: map[string]string{
				"app": "sctpserver",
			},
			Ports: []k8sv1.ServicePort{
				k8sv1.ServicePort{
					Protocol: k8sv1.ProtocolSCTP,
					Port:     30101,
					NodePort: 30101,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 30101,
					},
				},
			},
			Type: "NodePort",
		},
	}
	activeService, err := cs.Services(testNamespace).Create(&service)
	Expect(err).ToNot(HaveOccurred())
	return activeService
}

func deleteService(cs *client.ClientSet) {
	err := cs.Services(testNamespace).Delete("sctpservice", &metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0)})
	Expect(err).ToNot(HaveOccurred())
}

func scptTestPod(name, node, app string, args []string) *k8sv1.Pod {
	res := k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name,
			Labels: map[string]string{
				"app": app,
			},
			Namespace: testNamespace,
		},
		Spec: k8sv1.PodSpec{
			RestartPolicy: k8sv1.RestartPolicyNever,
			Containers: []k8sv1.Container{
				{
					Name:    name,
					Image:   testerImage,
					Command: []string{"/usr/bin/sctptest"},
					Args:    args,
				},
			},
			NodeSelector: map[string]string{
				hostnameLabel: node,
			},
		},
	}

	return &res
}

func jobForNode(name, node, app string, cmd []string, args []string) *k8sv1.Pod {
	job := k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name,
			Labels: map[string]string{
				"app": app,
			},
			Namespace: testNamespace,
		},
		Spec: k8sv1.PodSpec{
			RestartPolicy: k8sv1.RestartPolicyNever,
			Containers: []k8sv1.Container{
				{
					Name:    name,
					Image:   testerImage,
					Command: cmd,
					Args:    args,
				},
			},
			NodeSelector: map[string]string{
				hostnameLabel: node,
			},
		},
	}

	return &job
}
