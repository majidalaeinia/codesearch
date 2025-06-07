# Code Search
You can search your source codes and find out where which code is used.

## Usage
```bash
git clone git@github.com:majidalaeinia/codesearch.git
```

```bash
docker compose up -d
```

List your desired repositories on `repos.yaml` file.

```bash
go mod tidy
```

```bash
go run main.go
```

Open Kibana on the browser:
```
http://localhost:5601
```

Search your desired content on Kibana like so:
```bash
GET codesearch/_search
{
  "query": {
    "match": {
      "content": "your_desired_content"
    }
  }
}
```

Happy code searching.