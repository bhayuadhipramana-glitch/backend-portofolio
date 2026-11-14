// F1 read benchmark: public GET endpoints must hold p99 < 50 ms at 5,000 RPS.
//
// Run (API and its dependencies must already be up and seeded):
//   k6 run loadtest/f1_read_p99.js
//   k6 run -e BASE_URL=https://api.example.com -e PEAK_RPS=5000 loadtest/f1_read_p99.js
//   docker run --rm -i --network host -e BASE_URL=http://127.0.0.1:8080 grafana/k6 run - < loadtest/f1_read_p99.js
//
// Tunables (all optional): BASE_URL, PEAK_RPS (default 5000), HOLD (default 2m).
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const PEAK_RPS = parseInt(__ENV.PEAK_RPS || '5000', 10);
const HOLD = __ENV.HOLD || '2m';

const ENDPOINTS = [
  '/api/v1/projects',
  '/api/v1/articles',
  '/api/v1/profiles',
  '/api/v1/resumes',
];

// Where each response was served from; shows how well the cache absorbs the load.
const cacheL1 = new Counter('cache_hit_l1');
const cacheL2 = new Counter('cache_hit_l2');
const cacheMiss = new Counter('cache_miss');

export const options = {
  scenarios: {
    public_reads: {
      // An open model: arrivals keep coming even when the server slows down,
      // so latency problems cannot hide behind back-pressure.
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: Math.ceil(PEAK_RPS / 10),
      maxVUs: PEAK_RPS * 2,
      stages: [
        { target: Math.ceil(PEAK_RPS * 0.1), duration: '20s' }, // warm the caches
        { target: Math.ceil(PEAK_RPS * 0.5), duration: '30s' },
        { target: PEAK_RPS, duration: '30s' },
        { target: PEAK_RPS, duration: HOLD }, // sustained peak
        { target: 0, duration: '10s' },
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(99)<50'],
    http_req_failed: ['rate<0.001'],
    checks: ['rate>0.999'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  // Fail fast when the target is not serving, instead of reporting 100% errors.
  for (const path of ENDPOINTS) {
    const res = http.get(`${BASE_URL}${path}`);
    if (res.status !== 200) {
      throw new Error(`${path} returned ${res.status}; start the API and seed data first`);
    }
  }
}

export default function () {
  const path = ENDPOINTS[Math.floor(Math.random() * ENDPOINTS.length)];
  const res = http.get(`${BASE_URL}${path}`, { tags: { endpoint: path } });

  check(res, {
    'status is 200': (r) => r.status === 200,
    'has X-Cache header': (r) => !!r.headers['X-Cache'],
  });

  switch (res.headers['X-Cache']) {
    case 'HIT-L1':
      cacheL1.add(1);
      break;
    case 'HIT-L2':
      cacheL2.add(1);
      break;
    case 'MISS':
      cacheMiss.add(1);
      break;
  }
}
