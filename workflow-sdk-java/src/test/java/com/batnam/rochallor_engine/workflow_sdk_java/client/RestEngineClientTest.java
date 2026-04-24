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
                    {"jobs":[{"id":"j1","jobType":"my-job","instanceId":"i1","retriesRemaining":2}]}
                """)));

        List<Job> jobs = client.pollJobs("w1", List.of("my-job"), 1);

        assertEquals(1, jobs.size());
        assertEquals("j1", jobs.get(0).id);
        assertEquals("my-job", jobs.get(0).jobType);
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
}
