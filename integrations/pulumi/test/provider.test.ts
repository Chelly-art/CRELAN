import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import test from "node:test";
import { CrelanDeploymentProvider, type DeploymentInputs } from "../provider";

async function withApi(
    handler: (request: IncomingMessage, response: ServerResponse) => void | Promise<void>,
    run: (apiUrl: string) => Promise<void>,
): Promise<void> {
    let handlerError: unknown;
    const server = createServer((request, response) => {
        Promise.resolve(handler(request, response)).catch((error: unknown) => {
            handlerError = error;
            if (!response.headersSent) response.writeHead(500);
            response.end();
        });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("test API did not bind to a port");
    try {
        await run(`http://127.0.0.1:${address.port}`);
    } finally {
        await new Promise<void>((resolve, reject) =>
            server.close((error) => (error ? reject(error) : resolve())),
        );
    }
    if (handlerError) throw handlerError;
}

function inputs(apiUrl: string): DeploymentInputs {
    return {
        apiUrl,
        token: "test-token",
        name: "storefront",
        image: "registry.example/storefront:abc123",
        port: 8080,
        cpuMillicores: 500,
        memoryMb: 512,
        waitForReady: false,
        waitTimeoutSeconds: 60,
    };
}

test("create, update, and delete call the deployment API with expected payloads", async () => {
    let postCount = 0;
    await withApi(async (request, response) => {
        assert.equal(request.headers.authorization, "Bearer test-token");
        if (request.method === "POST" && request.url === "/api/v1/deployments") {
            let raw = "";
            for await (const chunk of request) raw += chunk;
            const body = JSON.parse(raw) as Record<string, unknown>;
            assert.equal(body.name, "storefront");
            assert.equal(
                body.image,
                postCount === 0
                    ? "registry.example/storefront:abc123"
                    : "registry.example/storefront:def456",
            );
            assert.equal(body.port, 8080);
            assert.equal(body.cpu_millicores, 500);
            assert.equal(body.memory_mb, 512);
            postCount += 1;
            response.writeHead(202, { "Content-Type": "application/json" });
            response.end(JSON.stringify({
                id: 41,
                name: "storefront",
                status: "queued",
                image: "registry.example/storefront:abc123",
                port: 8080,
                cpu_millicores: 500,
                memory_mb: 512,
            }));
            return;
        }
        if (request.method === "DELETE" && request.url === "/api/v1/deployments/41") {
            response.writeHead(204);
            response.end();
            return;
        }
        response.writeHead(404);
        response.end();
    }, async (apiUrl) => {
        const provider = new CrelanDeploymentProvider();
        const args = inputs(apiUrl);
        const created = await provider.create(args);
        assert.equal(created.id, "41");
        assert.equal(created.outs.status, "queued");

        await provider.update(created.id, created.outs as never, { ...args, image: "registry.example/storefront:def456" });
        await provider.delete(created.id, created.outs as never);
        assert.equal(postCount, 2);
    });
});

test("wait-for-ready polls until the deployment is running and supports source builds", async () => {
    let getCount = 0;
    await withApi(async (request, response) => {
        assert.equal(request.headers.authorization, "Bearer test-token");
        if (request.method === "POST") {
            let raw = "";
            for await (const chunk of request) raw += chunk;
            const body = JSON.parse(raw) as Record<string, unknown>;
            assert.equal(body.repository_url, "https://github.com/acme/storefront");
            assert.equal(body.branch, "main");
            response.writeHead(202, { "Content-Type": "application/json" });
            response.end(JSON.stringify({ id: 42, name: "storefront", status: "queued" }));
            return;
        }
        if (request.method === "GET" && request.url === "/api/v1/deployments/42") {
            getCount += 1;
            response.writeHead(200, { "Content-Type": "application/json" });
            response.end(JSON.stringify({ id: 42, name: "storefront", status: "running", url: "https://storefront.example" }));
            return;
        }
        response.writeHead(404);
        response.end();
    }, async (apiUrl) => {
        const provider = new CrelanDeploymentProvider();
        const args: DeploymentInputs = {
            ...inputs(apiUrl),
            image: undefined,
            repositoryUrl: "https://github.com/acme/storefront",
            branch: "main",
            waitForReady: true,
        };
        const result = await provider.create(args);
        assert.equal(result.id, "42");
        assert.equal(result.outs.status, "running");
        assert.equal(result.outs.url, "https://storefront.example");
        assert.equal(getCount, 1);
    });
});

test("diff replaces a deployment when its Crelan app name changes", async () => {
    const provider = new CrelanDeploymentProvider();
    const oldInputs = inputs("https://crelan.example");
    const oldOutputs = { ...oldInputs, deploymentId: "41", status: "running" };
    const unchanged = await provider.diff("41", oldOutputs, oldInputs);
    assert.equal(unchanged.changes, false);

    const renamed = await provider.diff("41", oldOutputs, { ...oldInputs, name: "storefront-next" });
    assert.deepEqual(renamed.replaces, ["name"]);
    assert.equal(renamed.deleteBeforeReplace, true);
});

test("input validation rejects unsafe URLs and invalid resource values", async () => {
    const provider = new CrelanDeploymentProvider();
    await assert.rejects(() => provider.create({ ...inputs("http://example.com"), waitForReady: false }), /HTTPS/);
    await assert.rejects(() => provider.create({ ...inputs("https://crelan.example"), port: 0 }), /port/);
    await assert.rejects(() => provider.create({ ...inputs("https://crelan.example"), waitTimeoutSeconds: 0 }), /waitTimeoutSeconds/);
});
