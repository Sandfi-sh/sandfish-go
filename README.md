# Sandfish Go SDK

The official Go client for the [Sandfish API](https://sandfi.sh/docs#api).
The module requires Go 1.23 or later.

## Install the preview

```sh
go get github.com/sandfi-sh/sandfish-go@main
```

Use a version tag when one is available. Create an API key at
<https://sandfi.sh/account/api>.

## Submit and wait

```go
client, err := sandfish.NewClient(sandfish.Options{
    APIKey: os.Getenv("SANDFISH_API_KEY"),
    MaxRetries: 2,
})
if err != nil { return err }

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()
request := sandfish.NewSubmitUrlRequest("https://example.com/")
request.SetVisibility(sandfish.ANALYSISVISIBILITY_PUBLIC)
accepted, _, err := client.AnalysesAPI.SubmitUrl(ctx).SubmitUrlRequest(*request).Execute()
if err != nil { return err }
operation, err := client.WaitForOperation(ctx, accepted.OperationId, time.Second)
if err != nil { return err }
fmt.Println(operation.Status)
```

Import the module as `sandfish "github.com/sandfi-sh/sandfish-go"`. Generated
methods return the typed value, HTTP response, and error. Keep the response
headers when reporting an error.

## Pagination and streaming

```go
err = client.ForEachAnalysis(ctx, 50, func(analysis sandfish.AnalysisSummary) error {
    fmt.Println(analysis.Id, analysis.Status)
    return nil
})
if err != nil { return err }

file, err := os.Create("capture.wacz")
if err != nil { return err }
defer file.Close()
_, err = client.Download(ctx, "/api/analyses/"+accepted.Id+"/wacz", file)
```

The download helper streams to an `io.Writer`. Generated download methods can
buffer the response and return a temporary file. Close and remove that file
after use.

## Configuration and errors

`Options` accepts `APIKey`, `BaseURL`, `HTTPClient`, and `MaxRetries`. The default
server is `https://sandfi.sh`; omit `/api` from the server URL. Omit the key for
public reads. The default HTTP client has a 30-second timeout.

Retries are disabled by default. A positive `MaxRetries` retries GET and HEAD
responses with HTTP 429, 502, 503, or 504. It respects `Retry-After` and context
cancellation. Submissions are sent once.

`OperationError` retains a failed or cancelled operation. Generated API errors
use `GenericOpenAPIError`; its `Model()` exposes the error response when the
body is JSON. Streaming errors use `HTTPError`, including non-JSON proxy errors.

Use the generated nullable types to distinguish omitted fields from explicit
null. An operation's success is separate from an analysis verdict. Read the
current evaluation and its evidence completeness.

## Development

```sh
go test ./...
go vet ./...
```

API methods and models are generated. The
[generation guide](https://github.com/sandfi-sh/openapi) owns updates.
`.sdk-generated.json` records source hashes and generated file ownership.
`helpers.go` contains the maintained client helpers.

[Examples](https://github.com/sandfi-sh/examples) ·
[API documentation](https://sandfi.sh/docs#api) ·
[Report a problem](https://github.com/sandfi-sh/sandfish-go/issues)

## License

MIT. See [LICENSE](LICENSE).
