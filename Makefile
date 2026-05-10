uv:
	cd churn && uv sync && uv run churn.py
	go run main.go -dir churn/golangci-lint -churn churn/churn.csv

python: 
	
	cd churn && pip install pydriller && python churn.py
	go run main.go -dir churn/golangci-lint -churn churn/churn.csv
