curl -u marathon:secret -H "Content-Type: application/json" -X PUT http://localhost:8081/v2/apps//python-http \
  -d '
    {
      "id": "/python-http",
      "cmd": "/usr/bin/python3.5 -m http.server $PORT0",
      "instances": 1,
      "cpus": 1,
      "mem": 128,
      "healthChecks": [
        {
          "gracePeriodSeconds": 300,
          "intervalSeconds": 60,
          "timeoutSeconds": 20,
          "maxConsecutiveFailures": 3,
          "portIndex": 0,
          "path": "/",
          "protocol": "HTTP",
          "ignoreHttp1xx": false
        }
      ],
      "portDefinitions": [
        {
          "port": 20000,
          "protocol": "tcp",
          "name": "http",
          "labels": {}
        }
      ]
    }
'