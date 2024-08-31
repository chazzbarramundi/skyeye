// package simpleradio contains a bespoke SimpleRadio-Standalone client.
package simpleradio

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dharmab/skyeye/pkg/simpleradio/audio"
	"github.com/dharmab/skyeye/pkg/simpleradio/data"
	"github.com/dharmab/skyeye/pkg/simpleradio/types"
	"github.com/martinlindhe/unit"
	"github.com/rs/zerolog/log"
)

// Client is a SimpleRadio-Standalone client.
type Client interface {
	// Name returns the name of the client as it appears in the SRS client list and in in-game transmissions.
	Name() string
	// Frequencies returns the frequencies the client is listening on.
	Frequencies() []RadioFrequency
	// Run starts the SimpleRadio-Standalone client. It should be called exactly once.
	Run(context.Context, *sync.WaitGroup) error
	// Receive returns a channel that receives transmissions over the radio. Each transmission is F32LE PCM audio data.
	Receive() <-chan audio.Audio
	// Transmit queues a transmission to send over the radio. The audio data should be in F32LE PCM format.
	Transmit(audio.Audio)
	// IsOnFrequency checks if the named unit is on any of the client's frequencies.
	IsOnFrequency(string) bool
	// ClientsOnFrequency returns the number of peers on the client's frequencies.
	ClientsOnFrequency() int
}

// client implements the SRS Client.
type client struct {
	// dataClient is a client for the SRS data protocol.
	dataClient data.DataClient
	// audioClient is a client for the SRS audio protocol.
	audioClient audio.AudioClient
}

func NewClient(config types.ClientConfiguration) (Client, error) {
	guid := types.NewGUID()
	dataClient, err := data.NewClient(guid, config)
	if err != nil {
		return nil, fmt.Errorf("failed to construct SRS data client: %w", err)
	}

	audioClient, err := audio.NewClient(guid, config)
	if err != nil {
		return nil, fmt.Errorf("failed to construct SRS audio client: %w", err)
	}

	client := &client{
		dataClient:  dataClient,
		audioClient: audioClient,
	}

	return client, nil
}

// Name implements [Client.Name].
func (c *client) Name() string {
	info := c.dataClient.Info()
	return info.Name
}

// Frequencies implements [Client.Frequencies].
func (c *client) Frequencies() []RadioFrequency {
	info := c.dataClient.Info()
	frequencies := make([]RadioFrequency, 0)
	for _, radio := range info.RadioInfo.Radios {
		frequency := RadioFrequency{
			Frequency:  unit.Frequency(radio.Frequency) * unit.Hertz,
			Modulation: radio.Modulation,
		}
		frequencies = append(frequencies, frequency)
	}
	return frequencies
}

// Run implements [Client.Run].
func (c *client) Run(ctx context.Context, wg *sync.WaitGroup) error {
	errorChan := make(chan error)

	// TODO return a ready channel and wait for each. This resolves a minor race condition on startup
	dataReadyCh := make(chan any)
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Msg("running SRS data client")
		if err := c.dataClient.Run(ctx, wg, dataReadyCh); err != nil {
			errorChan <- err
		}
	}()
	<-dataReadyCh

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Msg("running SRS audio client")
		if err := c.audioClient.Run(ctx, wg); err != nil {
			errorChan <- err
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("stopping SRS client due to context cancelation")
			return fmt.Errorf("stopping client due to context cancelation: %w", ctx.Err())
		case err := <-errorChan:
			return fmt.Errorf("client error: %w", err)
		case <-ticker.C:
			if time.Since(c.audioClient.LastPing()) > 1*time.Minute {
				log.Warn().Msg("stopped receiving pings from SRS data client")
				return errors.New("stopped receiving pings from SRS data client")
			}
		}
	}
}

// Receive implements [Client.Receive].
func (c *client) Receive() <-chan audio.Audio {
	return c.audioClient.Receive()
}

// Transmit implements [Client.Transmit].
func (c *client) Transmit(sample audio.Audio) {
	c.audioClient.Transmit(sample)
}

// IsOnFrequency implements [Client.IsOnFrequency].
func (c *client) IsOnFrequency(name string) bool {
	return c.dataClient.IsOnFrequency(name)
}

// ClientsOnFrequency implements [Client.ClientsOnFrequency].
func (c *client) ClientsOnFrequency() int {
	return c.dataClient.ClientsOnFrequency()
}
