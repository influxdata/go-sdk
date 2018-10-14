package configcat

import (
	"errors"
	"log"
	"os"
	"time"
)

// A client for handling configurations provided by ConfigCat.
type Client struct {
	configProvider				ConfigProvider
	store						*ConfigStore
	parser 						*ConfigParser
	refreshPolicy 				RefreshPolicy
	maxWaitTimeForSyncCalls 	time.Duration
	logger 						*log.Logger
}

// Describes custom configuration options for the Client.
type ClientConfig struct {
	// The factory delegate used to produce custom RefreshPolicy implementations.
	PolicyFactory 				func(configProvider ConfigProvider, store *ConfigStore) RefreshPolicy
	// The custom cache implementation used to store the configuration.
	Cache						ConfigCache
	// The maximum time how long at most the synchronous calls (e.g. client.Get(...)) should block the caller.
	// If it's 0 then the caller will be blocked in case of sync calls, until the operation succeeds or fails.
	MaxWaitTimeForSyncCalls		time.Duration
	// The maximum wait time for a http response.
	HttpTimeout					time.Duration
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		Cache: NewInMemoryConfigCache(),
		MaxWaitTimeForSyncCalls: 0,
		HttpTimeout: time.Second * 15,
		PolicyFactory: func(configProvider ConfigProvider, store *ConfigStore) RefreshPolicy {
			return NewAutoPollingPolicy(configProvider, store, time.Second * 120)
		},
	}
}

// NewClient initializes a new ConfigCat Client with the default configuration. The api key parameter is mandatory.
func NewClient(apiKey string) *Client {
	return NewCustomClient(apiKey, DefaultClientConfig())
}

// NewCustomClient initializes a new ConfigCat Client with advanced configuration. The api key parameter is mandatory.
func NewCustomClient(apiKey string, config ClientConfig) *Client {
	return newInternal(apiKey, config, newConfigFetcher(apiKey, config))
}

func newInternal(apiKey string, config ClientConfig, fetcher ConfigProvider) *Client {
	if len(apiKey) == 0 {
		panic("apiKey cannot be empty")
	}

	store := newConfigStore(config.Cache)
	policy := config.PolicyFactory(fetcher, store)
	return &Client{ configProvider: fetcher,
		store:store,
		parser:newParser(),
		refreshPolicy:policy,
		maxWaitTimeForSyncCalls:config.MaxWaitTimeForSyncCalls,
		logger: log.New(os.Stderr, "[ConfigCat - Config Cat Client]", log.LstdFlags)}
}

// Get returns a value synchronously as interface{} from the configuration identified by the given key.
func (client *Client) GetValue(key string, defaultValue interface{}) interface{} {
	return client.GetValueForUser(key, defaultValue, nil)
}

// GetAsync reads and sends a value asynchronously to a callback function as interface{} from the configuration identified by the given key.
func (client *Client) GetValueAsync(key string, defaultValue interface{}, completion func(result interface{})) {
	client.GetValueAsyncForUser(key, defaultValue, nil, completion)
}

// GetValueForUser returns a value synchronously as interface{} from the configuration identified by the given key.
// Optional user argument can be passed to identify the caller.
func (client *Client) GetValueForUser(key string, defaultValue interface{}, user *User) interface{} {
	if len(key) == 0 {
		panic("key cannot be empty")
	}

	json, err := client.getJsonFromPolicy()
	if err != nil {
		return client.getDefault(key, defaultValue, user)
	}

	parsed, err := client.parser.ParseWithUser(json, key, user)
	if err != nil {
		return client.getDefault(key, defaultValue, user)
	}

	return parsed
}

// GetAsyncForUser reads and sends a value asynchronously to a callback function as interface{} from the configuration identified by the given key.
// Optional user argument can be passed to identify the caller.
func (client *Client) GetValueAsyncForUser(key string, defaultValue interface{}, user *User, completion func(result interface{})) {
	if len(key) == 0 {
		panic("key cannot be empty")
	}

	client.refreshPolicy.GetConfigurationAsync().Accept(func(res interface{}) {
		parsed, err := client.parser.ParseWithUser(res.(string), key, user)
		if err != nil {
			completion(client.getDefault(key, defaultValue, user))
		}
		completion(parsed)
	})
}

// Refresh initiates a force refresh synchronously on the cached configuration.
func (client *Client) Refresh() {
	if client.maxWaitTimeForSyncCalls > 0 {
		client.refreshPolicy.RefreshAsync().WaitOrTimeout(client.maxWaitTimeForSyncCalls)
	} else {
		client.refreshPolicy.RefreshAsync().Wait()
	}
}

// RefreshAsync initiates a force refresh asynchronously on the cached configuration.
func (client *Client) RefreshAsync(completion func()) {
	client.refreshPolicy.RefreshAsync().Accept(completion)
}

// Close shuts down the client, after closing, it shouldn't be used
func (client *Client) Close() {
	client.refreshPolicy.Close()
}

func (client *Client) getJsonFromPolicy() (string, error) {
	if client.maxWaitTimeForSyncCalls > 0 {
		val, err := client.refreshPolicy.GetConfigurationAsync().GetOrTimeout(client.maxWaitTimeForSyncCalls)
		if err != nil {
			client.logger.Printf("Policy could not provide the configuration: %s", err.Error())
			return "", err
		}
		return val.(string), nil
	}

	val, ok := client.refreshPolicy.GetConfigurationAsync().Get().(string)
	if !ok {
		client.logger.Println("Policy could not provide the configuration")
		return "", errors.New("policy could not provide the configuration")
	}

	return val, nil
}

func (client *Client) getDefault(key string, defaultValue interface{}, user *User) interface{} {
	latest, parseErr := client.parser.ParseWithUser(client.store.inMemoryValue, key, user)
	if parseErr != nil {
		return defaultValue
	}
	return latest
}
