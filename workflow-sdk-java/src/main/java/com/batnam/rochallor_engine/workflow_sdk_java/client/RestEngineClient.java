package com.batnam.rochallor_engine.workflow_sdk_java.client;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpRequest.BodyPublishers;
import java.net.http.HttpResponse;
import java.net.http.HttpResponse.BodyHandlers;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * REST transport implementation of {@link EngineClient}.
 * Uses {@code java.net.http.HttpClient} (JDK 11+) — no third-party HTTP library.
 */
public class RestEngineClient implements EngineClient {

    private final String baseUrl;
    private final HttpClient http;
    private final ObjectMapper mapper;

    public RestEngineClient(String baseUrl) {
        this.baseUrl = baseUrl.replaceAll("/$", "");
        this.http = HttpClient.newHttpClient();
        this.mapper = new ObjectMapper();
    }

    @Override
    public List<Job> pollJobs(String workerId, List<String> jobTypes, int maxJobs) throws Exception {
        Map<String, Object> body = new HashMap<>();
        body.put("workerId", workerId);
        body.put("jobTypes", jobTypes);
        body.put("maxJobs", maxJobs);

        HttpResponse<String> resp = post("/v1/jobs/poll", mapper.writeValueAsString(body));
        if (resp.statusCode() != 200) {
            throw new RuntimeException("pollJobs failed: HTTP " + resp.statusCode() + " — " + resp.body());
        }
        Map<String, Object> envelope = mapper.readValue(resp.body(), new TypeReference<>() {});
        List<Map<String, Object>> rawJobs = (List<Map<String, Object>>) envelope.get("jobs");
        if (rawJobs == null) return List.of();
        return rawJobs.stream()
                .map(m -> mapper.convertValue(m, Job.class))
                .toList();
    }

    @Override
    public void completeJob(String jobId, String workerId, Map<String, Object> variables) throws Exception {
        Map<String, Object> body = new HashMap<>();
        body.put("workerId", workerId);
        if (variables != null) body.put("variables", variables);

        HttpResponse<String> resp = post("/v1/jobs/" + jobId + "/complete", mapper.writeValueAsString(body));
        if (resp.statusCode() != 204 && resp.statusCode() != 200) {
            throw new RuntimeException("completeJob " + jobId + " failed: HTTP " + resp.statusCode());
        }
    }

    @Override
    public void failJob(String jobId, String workerId, String errorMessage, boolean retryable) throws Exception {
        Map<String, Object> body = new HashMap<>();
        body.put("workerId", workerId);
        body.put("errorMessage", errorMessage);
        body.put("retryable", retryable);

        HttpResponse<String> resp = post("/v1/jobs/" + jobId + "/fail", mapper.writeValueAsString(body));
        if (resp.statusCode() != 204 && resp.statusCode() != 200) {
            throw new RuntimeException("failJob " + jobId + " failed: HTTP " + resp.statusCode());
        }
    }

    private HttpResponse<String> post(String path, String json) throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl + path))
                .header("Content-Type", "application/json")
                .POST(BodyPublishers.ofString(json))
                .build();
        return http.send(req, BodyHandlers.ofString());
    }
}
