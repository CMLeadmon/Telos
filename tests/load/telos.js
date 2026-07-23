// k6 load test script for Telos API capacity drill
export const options = {
  vus: 25,
  duration: "30m",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<750"],
  },
};

export default function () {
  // Scenario iterations
}
