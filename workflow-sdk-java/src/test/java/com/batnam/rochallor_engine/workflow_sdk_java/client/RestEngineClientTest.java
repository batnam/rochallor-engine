package com.batnam.rochallor_engine.workflow_sdk_java.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.github.tomakehurst.wiremock.WireMockServer;
import com.github.tomakehurst.wiremock.core.WireMockConfiguration;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static com.github.tomakehurst.wiremock.client.WireMock.*;
import static org.junit.jupiter.api.Assertions.*;

class RestEngineClientTest {

    private WireMockServer wm;
    private RestEngineClient client;
    private ObjectMapper mapper = new ObjectMapper();

    @BeforeEach
    void setUp() {
        wm = new WireMockServer(WireMockConfiguration.wireMockConfig().dynamicPort());
        wm.start();
        client = new RestEngineClient("http://localhost:" + wm.port());
    }

    @AfterEach
    void tearDown() {
        wm.stop();
    }

    @Test
    void pollJobsHappyPath() throws Exception {
        wm.stubFor(post(urlEqualTo("/v1/jobs/poll"))
                .willReturn(okJson("""
                    {"jobs":[{"id":"j1","jobType":"my-job","instanceId":"i1","retriesRemaining":2,"lockExpiresAt":"2026-10-11T00:00:30Z"}]}
                """)));

        List<Job> jobs = client.pollJobs("w1", List.of("my-job"), 1);

        assertEquals(1, jobs.size());
        assertEquals("j1", jobs.get(0).id);
        assertEquals("my-job", jobs.get(0).jobType);
        assertEquals(java.time.Instant.parse("2026-10-11T00:00:30Z"), jobs.get(0).lockExpiresAt);
    }

    @Test
    void pollJobsServerError() {
        wm.stubFor(post(urlEqualTo("/v1/jobs/poll"))
                .willReturn(serverError().withBody("{\"error\":\"oops\"}")));

        assertThrows(RuntimeException.class,
                () -> client.pollJobs("w1", List.of("my-job"), 1));
    }

    @Test
    void completeJobHappyPath() throws Exception {
        wm.stubFor(post(urlMatching("/v1/jobs/.*/complete"))
                .willReturn(noContent()));

        assertDoesNotThrow(() -> client.completeJob("j1", "w1", Map.of("result", "ok")));
    }

    @Test
    void failJobHappyPath() throws Exception {
        wm.stubFor(post(urlMatching("/v1/jobs/.*/fail"))
                .willReturn(noContent()));

        assertDoesNotThrow(() -> client.failJob("j1", "w1", "boom", true));
    }

    @Test
    void callbackHonorsTimeoutAndPreservesStatus() throws Exception {
        wm.stubFor(post(urlEqualTo("/v1/jobs/j1/complete")).willReturn(ok().withFixedDelay(300)));
        try (var timeout = CallDeadline.within(java.time.Duration.ofMillis(40))) {
            assertThrows(java.net.http.HttpTimeoutException.class, () -> client.completeJob("j1", "w", Map.of()));
        }
        wm.stubFor(post(urlEqualTo("/v1/jobs/j2/complete")).willReturn(forbidden()));
        HttpError error = assertThrows(HttpError.class, () -> client.completeJob("j2", "w", Map.of()));
        assertEquals(403, error.statusCode);
    }

    @Test void customLeaseAndHeartbeatContract() throws Exception {
        wm.stubFor(post(urlEqualTo("/v1/jobs/poll")).willReturn(okJson("{\"jobs\":[]}")));
        client.pollJobs("w", List.of("work"), 1, 60);
        wm.verify(postRequestedFor(urlEqualTo("/v1/jobs/poll")).withRequestBody(matchingJsonPath("$.leaseSeconds", equalTo("60"))));
        wm.stubFor(post(urlEqualTo("/v1/jobs/job/heartbeat")).willReturn(okJson("{\"accepted\":true,\"lockExpiresAt\":\"2026-10-11T12:00:00Z\"}")));
        assertEquals(java.time.Instant.parse("2026-10-11T12:00:00Z"), client.extendJobLease("job", "w", 60));
        wm.verify(postRequestedFor(urlEqualTo("/v1/jobs/job/heartbeat"))
            .withRequestBody(equalToJson("{\"workerId\":\"w\",\"leaseSeconds\":60}")));
    }

    @Test void heartbeatErrorMappingAndTimeout() throws Exception {
        for (int status : new int[]{404, 405, 501, 409, 503}) {
            String job = "job-" + status;
            wm.stubFor(post(urlEqualTo("/v1/jobs/" + job + "/heartbeat")).willReturn(aResponse().withStatus(status)));
            var error = assertThrows(Exception.class, () -> client.extendJobLease(job, "w", 30));
            if (status == 409) assertInstanceOf(LeaseLostException.class, error);
            else if (status == 503) assertInstanceOf(HttpError.class, error);
            else assertInstanceOf(HeartbeatUnsupportedException.class, error);
        }
        wm.stubFor(post(urlEqualTo("/v1/jobs/slow/heartbeat")).willReturn(ok().withFixedDelay(300)));
        try (var scope = CallDeadline.within(java.time.Duration.ofMillis(40))) {
            assertThrows(java.net.http.HttpTimeoutException.class, () -> client.extendJobLease("slow", "w", 30));
        }
        wm.stubFor(post(urlEqualTo("/v1/jobs/bad/heartbeat")).willReturn(okJson("{\"accepted\":false}")));
        assertThrows(IllegalArgumentException.class, () -> client.extendJobLease("bad", "w", 30));
    }
}
