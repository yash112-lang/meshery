package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"time"

	"github.com/layer5io/meshery/handlers"
	"github.com/layer5io/meshery/helpers"
	"github.com/layer5io/meshery/internal/graphql"
	"github.com/layer5io/meshery/internal/store"
	"github.com/layer5io/meshery/models"
	"github.com/layer5io/meshery/models/pattern"
	"github.com/layer5io/meshery/router"
	"github.com/layer5io/meshkit/broker/nats"
	"github.com/layer5io/meshkit/database"
	"github.com/layer5io/meshkit/logger"
	mesherykube "github.com/layer5io/meshkit/utils/kubernetes"
	meshsyncmodel "github.com/layer5io/meshsync/pkg/model"
	"github.com/spf13/viper"

	"github.com/vmihailenco/taskq/v3"
	"github.com/vmihailenco/taskq/v3/memqueue"
)

var (
	globalTokenForAnonymousResults string
	version                        = "Not Set"
	commitsha                      = "Not Set"
	releasechannel                 = "Not Set"
)

const (
	// DefaultProviderURL is the provider url for the "none" provider
	DefaultProviderURL = "https://meshery.layer5.io"
)

func main() {
	if globalTokenForAnonymousResults != "" {
		models.GlobalTokenForAnonymousResults = globalTokenForAnonymousResults
	}

	// Initialize Logger instance
	log, err := logger.New("meshery", logger.Options{
		Format: logger.SyslogLogFormat,
	})
	if err != nil {
		fmt.Println(fmt.Errorf("logger init failed, %s", err.Error()))
		os.Exit(1)
	}

	// operatingSystem, err := exec.Command("uname", "-s").Output()
	// if err != nil {
	// 	log.Error(err)
	// }

	ctx := context.Background()

	viper.AutomaticEnv()

	viper.SetDefault("PORT", 8080)
	viper.SetDefault("ADAPTER_URLS", "")
	viper.SetDefault("BUILD", version)
	viper.SetDefault("OS", "meshery")
	viper.SetDefault("COMMITSHA", commitsha)
	viper.SetDefault("RELEASE_CHANNEL", releasechannel)

	store.Initialize()

	// Register local OAM traits and workloads
	if err := pattern.RegisterMesheryOAMTraits(); err != nil {
		log.Error(err)
	}
	if err := pattern.RegisterMesheryOAMWorkloads(); err != nil {
		log.Error(err)
	}
	log.Info("Registered Meshery local Capabilities")

	// Get the channel
	log.Info("Meshery server current channel: ", releasechannel)

	home, err := os.UserHomeDir()
	if viper.GetString("USER_DATA_FOLDER") == "" {
		if err != nil {
			log.Error(err)
		}
		viper.SetDefault("USER_DATA_FOLDER", path.Join(home, ".meshery", "config"))
	}
	log.Debug("Using " + viper.GetString("USER_DATA_FOLDER") + " to store user data")

	if viper.GetString("KUBECONFIG_FOLDER") == "" {
		if err != nil {
			log.Error(err)
		}
		viper.SetDefault("KUBECONFIG_FOLDER", path.Join(home, ".kube"))
	}
	log.Debug("Using " + viper.GetString("KUBECONFIG_FOLDER") + " as the folder to look for kubeconfig file")

	//if viper.GetBool("DEBUG") {
	//	log.SetLevel(log.DebugLevel)
	//}

	adapterURLs := viper.GetStringSlice("ADAPTER_URLS")

	adapterTracker := helpers.NewAdaptersTracker(adapterURLs)
	queryTracker := helpers.NewUUIDQueryTracker()

	// Uncomment line below to generate a new UUID and force the user to login every time Meshery is started.
	// fileSessionStore := sessions.NewFilesystemStore("", []byte(uuid.NewV4().Bytes()))
	// fileSessionStore := sessions.NewFilesystemStore("", []byte("Meshery"))
	// fileSessionStore.MaxLength(0)

	QueueFactory := memqueue.NewFactory()
	mainQueue := QueueFactory.RegisterQueue(&taskq.QueueOptions{
		Name: "loadTestReporterQueue",
	})

	provs := map[string]models.Provider{}

	preferencePersister, err := models.NewMapPreferencePersister()
	if err != nil {
		log.Error(err)
	}
	defer preferencePersister.ClosePersister()

	smiResultPersister, err := models.NewBitCaskSmiResultsPersister(viper.GetString("USER_DATA_FOLDER"))
	if err != nil {
		log.Error(err)
	}
	defer smiResultPersister.CloseResultPersister()

	testConfigPersister, err := models.NewBitCaskTestProfilesPersister(viper.GetString("USER_DATA_FOLDER"))
	if err != nil {
		log.Error(err)
	}
	defer testConfigPersister.CloseTestConfigsPersister()

	dbHandler, err := database.New(database.Options{
		Filename: fmt.Sprintf("%s/mesherydb.sql", viper.GetString("USER_DATA_FOLDER")),
		Engine:   database.SQLITE,
		Logger:   log,
	})
	if err != nil {
		log.Error(err)
	}

	kubeclient := mesherykube.Client{}
	meshsyncCh := make(chan struct{})
	brokerConn := nats.NewEmptyConnection

	err = dbHandler.AutoMigrate(
		meshsyncmodel.KeyValue{},
		meshsyncmodel.Object{},
		models.PerformanceProfile{},
		models.MesheryResult{},
		models.MesheryPattern{},
		models.MesheryFilter{},
		models.PatternResource{},
		models.MesheryApplication{},
	)
	if err != nil {
		log.Error(err)
	}

	lProv := &models.DefaultLocalProvider{
		ProviderBaseURL:                 DefaultProviderURL,
		MapPreferencePersister:          preferencePersister,
		ResultPersister:                 &models.MesheryResultsPersister{DB: &dbHandler},
		SmiResultPersister:              smiResultPersister,
		TestProfilesPersister:           testConfigPersister,
		PerformanceProfilesPersister:    &models.PerformanceProfilePersister{DB: &dbHandler},
		MesheryPatternPersister:         &models.MesheryPatternPersister{DB: &dbHandler},
		MesheryFilterPersister:          &models.MesheryFilterPersister{DB: &dbHandler},
		MesheryApplicationPersister:     &models.MesheryApplicationPersister{DB: &dbHandler},
		MesheryPatternResourcePersister: &models.PatternResourcePersister{DB: &dbHandler},
		GenericPersister:                dbHandler,
	}
	lProv.Initialize()
	provs[lProv.Name()] = lProv

	cPreferencePersister, err := models.NewBitCaskPreferencePersister(viper.GetString("USER_DATA_FOLDER"))
	if err != nil {
		log.Error(err)
	}
	defer preferencePersister.ClosePersister()

	RemoteProviderURLs := viper.GetStringSlice("PROVIDER_BASE_URLS")
	for _, providerurl := range RemoteProviderURLs {
		parsedURL, err := url.Parse(providerurl)
		if err != nil {
			log.Debug(providerurl, "is invalid url skipping provider")
			continue
		}
		cp := &models.RemoteProvider{
			RemoteProviderURL:          parsedURL.String(),
			RefCookieName:              parsedURL.Host + "_ref",
			SessionName:                parsedURL.Host,
			TokenStore:                 make(map[string]string),
			LoginCookieDuration:        1 * time.Hour,
			BitCaskPreferencePersister: cPreferencePersister,
			ProviderVersion:            "v0.3.14",
			SmiResultPersister:         smiResultPersister,
			GenericPersister:           dbHandler,
		}

		cp.Initialize()

		cp.SyncPreferences()
		defer cp.StopSyncPreferences()
		provs[cp.Name()] = cp
	}

	hc := &models.HandlerConfig{
		Providers:              provs,
		ProviderCookieName:     "meshery-provider",
		ProviderCookieDuration: 30 * 24 * time.Hour,

		AdapterTracker: adapterTracker,
		QueryTracker:   queryTracker,

		Queue: mainQueue,

		KubeConfigFolder: viper.GetString("KUBECONFIG_FOLDER"),
		KubeClient:       &kubeclient,

		GrafanaClient:         models.NewGrafanaClient(),
		GrafanaClientForQuery: models.NewGrafanaClientWithHTTPClient(&http.Client{Timeout: time.Second}),

		PrometheusClient:         models.NewPrometheusClient(),
		PrometheusClientForQuery: models.NewPrometheusClientWithHTTPClient(&http.Client{Timeout: time.Second}),
	}

	h := handlers.NewHandlerInstance(hc, meshsyncCh, log, brokerConn)

	g := graphql.New(graphql.Options{
		Config:          hc,
		Logger:          log,
		MeshSyncChannel: meshsyncCh,
		BrokerConn:      brokerConn,
	})

	gp := graphql.NewPlayground(graphql.Options{
		URL: "/api/system/graphql/query",
	})

	port := viper.GetInt("PORT")
	r := router.NewRouter(ctx, h, port, g, gp)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		log.Info("Starting Server listening on: " + string(port))
		if err := r.Run(); err != nil {
			log.Error(err)
		}
	}()
	<-c
	log.Info("Shutting down Meshery")
}
