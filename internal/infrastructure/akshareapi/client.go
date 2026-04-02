package akshareapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/bytedance/sonic"
)

const (
	publicAPIURI       = "/api/public/"
	defaultMaxAttempts = 10
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func NewConfiguredClient() (*Client, error) {
	cfg := config.Get().AKToolConfig
	if cfg == nil || cfg.BaseURL == "" {
		return nil, errors.New("akshareapi config missing or base url empty")
	}
	return NewClient(cfg.BaseURL, nil), nil
}

func (c *Client) CallRows(ctx context.Context, endpoint Endpoint, params any) (Rows, error) {
	var rows Rows
	if err := c.CallInto(ctx, endpoint, params, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (c *Client) CallByName(ctx context.Context, endpointName string, params any) (Rows, error) {
	endpoint, ok := EndpointByName(endpointName)
	if !ok {
		return nil, fmt.Errorf("endpoint %q not found", endpointName)
	}
	return c.CallRows(ctx, endpoint, params)
}

func (c *Client) CallInto(ctx context.Context, endpoint Endpoint, params any, out any) error {
	values, err := encodeQueryValues(params)
	if err != nil {
		return err
	}
	requestURL := c.baseURL + publicAPIURI + endpoint.Name
	if encoded := values.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}

	var lastErr error
	for attempt := 1; attempt <= defaultMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == defaultMaxAttempts {
				return err
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == defaultMaxAttempts {
				return readErr
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return sonic.Unmarshal(body, out)
		}

		lastErr = formatHTTPError(endpoint.Name, resp.StatusCode, body)
		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < defaultMaxAttempts {
			continue
		}
		return lastErr
	}
	return lastErr
}

func (c *Client) callRows(ctx context.Context, endpoint Endpoint, params any) (Rows, error) {
	return c.CallRows(ctx, endpoint, params)
}

func encodeQueryValues(params any) (url.Values, error) {
	values := url.Values{}
	if params == nil {
		return values, nil
	}

	raw := reflect.ValueOf(params)
	if !raw.IsValid() {
		return values, nil
	}
	for raw.Kind() == reflect.Pointer {
		if raw.IsNil() {
			return values, nil
		}
		raw = raw.Elem()
	}

	switch raw.Kind() {
	case reflect.Map:
		iter := raw.MapRange()
		for iter.Next() {
			values.Set(fmt.Sprint(iter.Key().Interface()), fmt.Sprint(iter.Value().Interface()))
		}
		return values, nil
	case reflect.Struct:
		rawType := raw.Type()
		for i := 0; i < raw.NumField(); i++ {
			fieldType := rawType.Field(i)
			if !fieldType.IsExported() {
				continue
			}
			key := fieldType.Tag.Get("query")
			if key == "" {
				continue
			}
			fieldValue := raw.Field(i)
			if isZeroValue(fieldValue) {
				continue
			}
			addQueryValue(values, key, fieldValue)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported params type %T", params)
	}
}

func addQueryValue(values url.Values, key string, value reflect.Value) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		values.Set(key, value.String())
	case reflect.Bool:
		values.Set(key, strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		values.Set(key, strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		values.Set(key, strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		values.Set(key, strconv.FormatFloat(value.Float(), 'f', -1, 64))
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			values.Add(key, fmt.Sprint(value.Index(i).Interface()))
		}
	default:
		values.Set(key, fmt.Sprint(value.Interface()))
	}
}

func isZeroValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return value.IsNil()
	default:
		return value.IsZero()
	}
}

func formatHTTPError(endpointName string, statusCode int, body []byte) error {
	preview := string(body)
	if len(preview) > 512 {
		preview = preview[:512]
	}
	return fmt.Errorf("akshareapi %s returned %d: %s", endpointName, statusCode, preview)
}
