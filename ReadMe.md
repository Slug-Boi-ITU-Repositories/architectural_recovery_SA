# Running the tools

# Test coverage report
To see the test coverage report of golangci-lint you have to have go installed and install the cover tool

```bash
go get golang.org/x/tools/cmd/cover
go tool cover -html=cover.out
```
If you want to generate the coverage yourself please fetch the golangci-lint repo and run the following command from the repo root folder

```bash
go test -coverprofile cover.out ./test/... 
```
