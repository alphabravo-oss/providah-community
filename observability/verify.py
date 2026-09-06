#!/usr/bin/env python3
"""Local development smoke: no credentials or cloud calls; requires `make observe`."""
import json
import time
import urllib.parse
import urllib.request
import base64

def read(path):
    request = urllib.request.Request('http://127.0.0.1:8761' + path)
    request.add_header('Authorization', 'Basic ' + base64.b64encode(b'admin:admin').decode())
    with urllib.request.urlopen(request, timeout=5) as response:
        return json.load(response)

# An unknown public path exercises the sanitized `other` route and creates a trace/log.
with urllib.request.urlopen('http://127.0.0.1:8760/observability-smoke', timeout=5) as response:
    request_id = response.headers['X-Request-ID']
for attempt in range(12):
    try:
        metric = read('/api/datasources/proxy/uid/prometheus/api/v1/query?' + urllib.parse.urlencode({'query': 'up{job="providah"}'}))
        assert metric['data']['result'][0]['value'][1] == '1'
        trace = read('/api/datasources/proxy/uid/tempo/api/search?' + urllib.parse.urlencode({'tags': 'providah.request_id=' + request_id}))
        assert trace.get('traces')
        logs = read('/api/datasources/proxy/uid/loki/loki/api/v1/query_range?' + urllib.parse.urlencode({'query': '{service_name="providah"} |= "' + request_id + '"', 'limit': 10}))
        assert logs['data']['result']
        assert read('/api/dashboards/uid/providah')['dashboard']['title'] == 'Providah operations'
        print('Metrics scrape, trace, matching request log, and provisioned dashboard verified.')
        break
    except (AssertionError, KeyError, IndexError, OSError):
        if attempt == 11:
            raise
        time.sleep(3)
