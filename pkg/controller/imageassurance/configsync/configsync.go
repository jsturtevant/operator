// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package configsync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strconv"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tigera/operator/pkg/common"
	"github.com/tigera/operator/pkg/controller/utils"
	rcimageassurance "github.com/tigera/operator/pkg/render/common/imageassurance"
	"github.com/tigera/operator/pkg/render/imageassurance"
	operatortime "github.com/tigera/operator/pkg/time"

	"github.com/tigera/operator/pkg/bastapi"
)

var log = logf.Log.WithName("controller_image_assurance_config_sync")

const (
	defaultReSyncDuration = 1 * time.Minute
	httpRequestTimeout    = 5 * time.Second
)

type BastClientCreator func(httpClient *http.Client, baseURL string, token string) bastapi.Client

type TickerCreator func(duration time.Duration) operatortime.Ticker

// syncer implements Syncer. When syncer is created (via the "NewSyncer" function), "run()" is immediately invoked. At
// this point it doesn't start polling the IA API, it's waiting for StartPeriodicSync to be called, which signals that
// it should immediately do a sync then periodically sync.
//
// syncer waits for the call the StartPeriodicSync so that the controller that sets up Image Assurance can set up the
// API and propagate credentials. This avoids avoidable failures while polling for settings (why poll when you know the
// API isn't ready).
type syncer struct {
	ctx      context.Context
	endpoint string
	client   client.Client

	reSyncDuration time.Duration

	bastClientCreator BastClientCreator
	tickerCreator     TickerCreator

	apiChan chan interface{}

	// If this channel is closed, then this syncer has stopped running (via the passed in context timing out or being
	// cancelled).
	done chan struct{}
}

// startSyncRequest is used to start syncing the config map.
type startSyncRequest struct {
}

// getErrRequest is used to get the error occurred (if any) from the syncer routine.
type getErrRequest struct {
	errChan chan error
}

// Syncer implementations should poll periodically for configuration stored in the IA database and cache them in the
// config map that image assurance stores its configuration in.
type Syncer interface {
	StartPeriodicSync()
	Error() error
}

func NewSyncer(ctx context.Context, endpoint string, client client.Client, options ...Option) Syncer {
	syn := &syncer{
		ctx:      ctx,
		endpoint: endpoint,
		client:   client,

		reSyncDuration: defaultReSyncDuration,

		bastClientCreator: bastapi.NewClient,
		tickerCreator:     operatortime.NewTicker,

		apiChan: make(chan interface{}, 1),
		done:    make(chan struct{}),
	}

	for _, option := range options {
		option(syn)
	}

	go syn.run()

	return syn
}

// StartPeriodicSync sends a signal to start polling for IA configuration and write it to the config map.
// Note that it is invalid to call this function after the context given to the syncer has been cancelled, as all the
// channels will have been closed.
func (s *syncer) StartPeriodicSync() {
	select {
	case s.apiChan <- startSyncRequest{}:
	default:
	}
}

func (s *syncer) Error() error {
	errChan := make(chan error)
	s.apiChan <- getErrRequest{errChan}

	return <-errChan
}

func (s *syncer) run() {
	// ticker and tickerCh are separated out because ticker is initially nil when the select is hit because we don't
	// want to start the ticker until a signal is sent over the start channel.
	var ticker operatortime.Ticker
	var tickerCh <-chan time.Time

	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	defer close(s.apiChan)
	defer close(s.done)

	var err error
	for {
		select {
		case req := <-s.apiChan:
			switch typedReq := req.(type) {
			case startSyncRequest:
				if ticker != nil {
					// We've already started the periodic sync so ignore any requests.
					continue
				}

				err = s.syncConfigMap()
				// Log the error in case it takes awhile for the reconcile loop to pick it up.
				if err != nil {
					log.Error(err, "Failed to sync Image Assurance config map with API.")
				}

				if ticker == nil {
					ticker = s.tickerCreator(s.reSyncDuration)
					tickerCh = ticker.Chan()
				}
			case getErrRequest:
				typedReq.errChan <- err
				close(typedReq.errChan)
			}
		case _, ok := <-tickerCh:
			// It's unlikely that we'll hit this case with ok set to false (i.e. the ticker chan has been closed), but
			// it is possible, so we have logic here just to treat it like we've stopped the ticker. Another call to
			// StartPeriodicSync would restart the ticker.
			if !ok {
				if ticker != nil {
					ticker.Stop()
				}

				ticker = nil
				tickerCh = nil

				continue
			}

			err = s.syncConfigMap()
			// Log the error in case it takes awhile for the reconcile loop to pick it up.
			if err != nil {
				log.Error(err, "Failed to sync Image Assurance config map with API.")
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *syncer) syncConfigMap() error {
	configurationConfigMap, err := utils.GetImageAssuranceConfigurationConfigMap(s.client)
	if err != nil {
		return fmt.Errorf("failed to retrieve image assurance configuration: %w", err)
	}

	apiToken, err := utils.GetImageAssuranceAPIAccessToken(s.client, imageassurance.OperatorAPIAccessServiceAccountName)
	if err != nil {
		return fmt.Errorf("failed to retrieve API token: %w", err)
	} else if len(apiToken) == 0 {
		return fmt.Errorf("API token not available")
	}

	certBytes, err := getAPICertificate(s.client)
	if err != nil {
		return fmt.Errorf("failed to get Image Assurance API certificate: %w", err)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(certBytes)

	bastAPIClient := s.bastClientCreator(&http.Client{
		Timeout: httpRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}, s.endpoint, string(apiToken))

	org, err := bastAPIClient.GetOrganization(configurationConfigMap.Data[rcimageassurance.ConfigurationConfigMapOrgIDKey])
	if err != nil {
		return fmt.Errorf("failed to get organization settings: %w", err)
	}

	configurationConfigMap.Data["runtimeViewEnabled"] = strconv.FormatBool(org.Settings.RuntimeViewEnabled)
	if err := s.client.Update(s.ctx, configurationConfigMap); err != nil {
		return fmt.Errorf("failed to update Image Assurance configuration: %w", err)
	}

	return nil
}

// getAPICertificate retrieves and returns the image assurance api tls certificate (as bytes) stored in the k8s secret.
func getAPICertificate(client client.Client) ([]byte, error) {
	secret := &corev1.Secret{}
	secretNamespacedName := types.NamespacedName{
		Name:      imageassurance.APICertSecretName,
		Namespace: common.OperatorNamespace(),
	}
	err := client.Get(context.Background(), secretNamespacedName, secret)
	if err != nil {
		return nil, err
	}

	return secret.Data[corev1.TLSCertKey], nil
}