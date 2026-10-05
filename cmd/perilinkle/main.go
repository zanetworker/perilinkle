package main

import (
	"flag"
	"os"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"github.com/gsim/perilinkle/internal/controller"
	"github.com/gsim/perilinkle/internal/keycloak"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr              string
		probeAddr                string
		leaderElection           bool
		watchNamespace           string
		controllerNamespace      string
		keycloakBaseURL          string
		keycloakPublicURL        string
		keycloakRealm            string
		keycloakAdminRealm       string
		keycloakAdminUser        string
		keycloakAdminPass        string
		keycloakAdminClientID    string
		keycloakAdminClientSec   string
		spiffeTrustDomain        string
		spiffeBundleEndpoint     string
		gatewayClientID          string
		gatewayAPIClientID       string
		gatewaySPIFFESubject     string
		cliClientID              string
		usersConfigMap           string
		usersSecret              string
		openShellGatewayAddr     string
		openShellGatewayCAFile   string
		openShellGatewayInsecure bool
		sandboxAPIVersion        string
		sandboxSPIFFEIDTemplate  string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "Namespace to watch. Empty watches all namespaces.")
	flag.StringVar(&controllerNamespace, "controller-namespace", envOrDefault("POD_NAMESPACE", "openshell"), "Namespace where the controller runs.")
	flag.StringVar(&keycloakBaseURL, "keycloak-url", envOrDefault("KEYCLOAK_URL", "http://keycloak.openshell.svc.cluster.local"), "Keycloak base URL.")
	flag.StringVar(&keycloakPublicURL, "keycloak-public-url", os.Getenv("KEYCLOAK_PUBLIC_URL"), "Optional public Keycloak base URL used in generated OpenShell provider profiles.")
	flag.StringVar(&keycloakRealm, "keycloak-realm", envOrDefault("KEYCLOAK_REALM", controller.DefaultRealm), "Keycloak realm to reconcile.")
	flag.StringVar(&keycloakAdminRealm, "keycloak-admin-realm", envOrDefault("KEYCLOAK_ADMIN_REALM", "master"), "Keycloak admin realm.")
	flag.StringVar(&keycloakAdminUser, "keycloak-admin-user", envOrDefault("KEYCLOAK_ADMIN_USER", "admin"), "Keycloak admin username.")
	flag.StringVar(&keycloakAdminPass, "keycloak-admin-password", os.Getenv("KEYCLOAK_ADMIN_PASSWORD"), "Keycloak admin password.")
	flag.StringVar(&keycloakAdminClientID, "keycloak-admin-client-id", os.Getenv("KEYCLOAK_ADMIN_CLIENT_ID"), "Optional Keycloak service-account client (client_credentials) used instead of the admin user password.")
	flag.StringVar(&keycloakAdminClientSec, "keycloak-admin-client-secret", os.Getenv("KEYCLOAK_ADMIN_CLIENT_SECRET"), "Secret for --keycloak-admin-client-id.")
	flag.StringVar(&spiffeTrustDomain, "spiffe-trust-domain", envOrDefault("SPIFFE_TRUST_DOMAIN", "spiffe://openshell.local"), "SPIFFE trust domain URI.")
	flag.StringVar(&spiffeBundleEndpoint, "spiffe-bundle-endpoint", envOrDefault("SPIFFE_BUNDLE_ENDPOINT", "https://spire-spiffe-oidc-discovery-provider.spire.svc.cluster.local/keys"), "SPIFFE OIDC bundle endpoint.")
	flag.StringVar(&gatewayClientID, "gateway-client-id", envOrDefault("GATEWAY_CLIENT_ID", "openshell-gateway"), "OpenShell gateway Keycloak client ID.")
	flag.StringVar(&gatewayAPIClientID, "gateway-api-client-id", envOrDefault("GATEWAY_API_CLIENT_ID", "perilinkle-openshell-gateway"), "Keycloak client ID used by perilinkle to call the OpenShell Gateway API.")
	flag.StringVar(&gatewaySPIFFESubject, "gateway-spiffe-subject", envOrDefault("GATEWAY_SPIFFE_SUBJECT", "spiffe://openshell.local/ns/openshell/sa/openshell"), "OpenShell gateway SPIFFE subject.")
	flag.StringVar(&cliClientID, "cli-client-id", envOrDefault("CLI_CLIENT_ID", "openshell-cli"), "OpenShell CLI Keycloak client ID.")
	flag.StringVar(&usersConfigMap, "users-configmap", os.Getenv("USERS_CONFIGMAP"), "Optional ConfigMap containing prototype user definitions.")
	flag.StringVar(&usersSecret, "users-secret", os.Getenv("USERS_SECRET"), "Optional Secret containing prototype user initial passwords.")
	flag.StringVar(&openShellGatewayAddr, "openshell-gateway-address", os.Getenv("OPENSHELL_GATEWAY_ADDRESS"), "Optional OpenShell Gateway gRPC address used for provider profile upsert.")
	flag.StringVar(&openShellGatewayCAFile, "openshell-gateway-ca-file", os.Getenv("OPENSHELL_GATEWAY_CA_FILE"), "Optional PEM CA bundle used to verify the OpenShell Gateway TLS certificate.")
	flag.BoolVar(&openShellGatewayInsecure, "openshell-gateway-insecure", envBoolOrDefault("OPENSHELL_GATEWAY_INSECURE", false), "Use insecure transport for OpenShell Gateway gRPC.")

	flag.StringVar(&sandboxAPIVersion, "sandbox-api-version", os.Getenv("SANDBOX_API_VERSION"), "Agent Sandbox API version to watch. Empty auto-detects (prefers v1beta1, then v1alpha1).")

	flag.StringVar(&sandboxSPIFFEIDTemplate, "sandbox-spiffe-id-template", envOrDefault("SANDBOX_SPIFFE_ID_TEMPLATE", controller.DefaultSPIFFEIDTemplate), "Sandbox supervisor SPIFFE ID template; must match the ClusterSPIFFEID. Placeholders: {trustDomain} {namespace} {name} {sandboxID}.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	managerOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "perilinkle.dev",
	}
	if watchNamespace != "" {
		managerOptions.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{watchNamespace: {}}}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions)
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	spiffeIDTemplate, err := controller.NewSPIFFEIDTemplate(sandboxSPIFFEIDTemplate)
	if err != nil {
		ctrl.Log.Error(err, "invalid sandbox SPIFFE ID template")
		os.Exit(1)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		ctrl.Log.Error(err, "unable to create discovery client")
		os.Exit(1)
	}
	sandboxGVK, err := controller.ResolveSandboxGVK(discoveryClient, sandboxAPIVersion)
	if err != nil {
		ctrl.Log.Error(err, "unable to resolve Agent Sandbox API version")
		os.Exit(1)
	}
	ctrl.Log.Info("watching Agent Sandbox", "gvk", sandboxGVK.String())

	keycloakClient := keycloak.NewClient(keycloak.Config{
		BaseURL:              keycloakBaseURL,
		Realm:                keycloakRealm,
		AdminRealm:           keycloakAdminRealm,
		AdminUsername:        keycloakAdminUser,
		AdminPassword:        keycloakAdminPass,
		AdminClientID:        keycloakAdminClientID,
		AdminClientSecret:    keycloakAdminClientSec,
		SPIFFETrustDomain:    spiffeTrustDomain,
		SPIFFEBundleEndpoint: spiffeBundleEndpoint,
		GatewayClientID:      gatewayClientID,
		GatewayAPIClientID:   gatewayAPIClientID,
		GatewaySPIFFESubject: gatewaySPIFFESubject,
		CLIClientID:          cliClientID,
	})
	openShellGateway, err := controller.NewOpenShellGatewayClient(controller.OpenShellGatewayConfig{
		Address:     openShellGatewayAddr,
		Insecure:    openShellGatewayInsecure,
		CAFile:      openShellGatewayCAFile,
		TokenSource: keycloakClient,
		Realm:       keycloak.Realm{Name: keycloakRealm},
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to create openshell gateway client")
		os.Exit(1)
	}

	if err := mgr.Add(&controller.RealmBootstrapper{
		Keycloak: keycloakClient,
		Realm:    keycloakRealm,
	}); err != nil {
		ctrl.Log.Error(err, "unable to add realm bootstrapper")
		os.Exit(1)
	}

	if err := (&controller.SandboxReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Keycloak: keycloakClient,
		Config: controller.SandboxConfig{
			ManagedLabel:      controller.LabelManaged,
			ManagedLabelValue: "openshell",
			Realm:             keycloakRealm,
			SPIFFETrustDomain: spiffeTrustDomain,
			SandboxGVK:        sandboxGVK,
			SPIFFEIDTemplate:  spiffeIDTemplate,
		},
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create sandbox controller")
		os.Exit(1)
	}

	if err := (&controller.ServiceGroupReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Keycloak:         keycloakClient,
		OpenShellGateway: openShellGateway,
		Config: controller.ServiceGroupConfig{
			ManagedLabel:      controller.LabelManaged,
			ManagedLabelValue: "openshell",
			KeycloakURL:       keycloakBaseURL,
			KeycloakPublicURL: keycloakPublicURL,
			Realm:             keycloakRealm,
			SPIFFETrustDomain: spiffeTrustDomain,
			SandboxGVK:        sandboxGVK,
			SPIFFEIDTemplate:  spiffeIDTemplate,
		},
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create service group controller")
		os.Exit(1)
	}

	if usersConfigMap != "" {
		if err := mgr.Add(&controller.UserBootstrapper{
			Client:   mgr.GetClient(),
			Keycloak: keycloakClient,
			Config: controller.UserBootstrapConfig{
				Namespace:     controllerNamespace,
				ConfigMapName: usersConfigMap,
				SecretName:    usersSecret,
				Realm:         keycloakRealm,
			},
		}); err != nil {
			ctrl.Log.Error(err, "unable to add user bootstrapper")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctrl.Log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBoolOrDefault(name string, fallback bool) bool {
	switch os.Getenv(name) {
	case "true", "1", "yes", "y":
		return true
	case "false", "0", "no", "n":
		return false
	default:
		return fallback
	}
}
