# Running the tools

# Makefile
There is a makefile in the repo that allows you run the combined output of the Python churn tool and dependency graph tool using out uv or just Python.
Requirements:
- uv (optional)
- Python
- Go

```bash
# uv package manager version
$ make uv

# Pure python version
$ make python
```

# Manual run
To run the dependency tool manually you can run the below commands you will need a churn.csv output file from the Python script. The dir flag is the directory of the golangci-lint repo and the churn flag is the location of the churn.csv file
```bash
$ go run main.go -dir churn/golangci-lint -churn churn/churn.csv
```

To run the Python script to get the churn.csv output file you can run the following

```bash
# Using uv
$ python churn.py

# Using Python
$ uv run churn.py
```


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
