import * as pulumi from "@pulumi/pulumi";

interface DeploymentInputs {
    apiUrl: string;
    token: string;
    name: string;
    image?: string;
    imagePullSecret?: string;
    repositoryUrl?: string;
    branch?: string;
    dockerfile?: string;
    port: number;
    cpuMillicores: number;
    memoryMb: number;
    waitForReady: boolean;
    waitTimeoutSeconds: number;
}

interface DeploymentState {
    id: number;
    name: string;
    status: string;
    url?: string;
}

interface DeploymentOutputs extends DeploymentInputs {
    deploymentId: string;
    status: string;
    url?: string;
}

export interface DeploymentArgs {
    apiUrl: pulumi.Input<string>;
    token: pulumi.Input<string>;
    name: pulumi.Input<string>;
    image?: pulumi.Input<string>;
    imagePullSecret?: pulumi.Input<string>;
    repositoryUrl?: pulumi.Input<string>;
    branch?: pulumi.Input<string>;
    dockerfile?: pulumi.Input<string>;
    port?: pulumi.Input<number>;
    cpuMillicores?: pulumi.Input<number>;
    memoryMb?: pulumi.Input<number>;
    waitForReady?: pulumi.Input<boolean>;
    waitTimeoutSeconds?: pulumi.Input<number>;
}

class CrelanDeploymentProvider implements pulumi.dynamic.ResourceProvider {
    async create(inputs: DeploymentInputs): Promise<pulumi.dynamic.CreateResult> {
        validateInputs(inputs);
        const created = await createDeployment(inputs);
        const deployment = inputs.waitForReady
            ? await waitForDeployment(inputs, created.id)
            : created;
        return {
            id: String(created.id),
            outs: outputsFor(inputs, deployment),
        };
    }

    async diff(
        _id: string,
        olds: DeploymentOutputs,
        news: DeploymentInputs,
    ): Promise<pulumi.dynamic.DiffResult> {
        const changed = Object.keys(news).filter((key) =>
            JSON.stringify((olds as unknown as Record<string, unknown>)[key]) !==
            JSON.stringify((news as unknown as Record<string, unknown>)[key]),
        );
        const nameChanged = changed.includes("name");
        return {
            changes: changed.length > 0,
            replaces: nameChanged ? ["name"] : [],
            deleteBeforeReplace: nameChanged,
        };
    }

    async update(
        _id: string,
        _olds: DeploymentOutputs,
        news: DeploymentInputs,
    ): Promise<pulumi.dynamic.UpdateResult> {
        validateInputs(news);
        const created = await createDeployment(news);
        const deployment = news.waitForReady
            ? await waitForDeployment(news, created.id)
            : created;
        return { outs: outputsFor(news, deployment) };
    }

    async delete(id: string, props: DeploymentOutputs): Promise<void> {
        await request(props.apiUrl, props.token, "DELETE", `/api/v1/deployments/${encodeURIComponent(id)}`);
    }
}

function validateInputs(inputs: DeploymentInputs): void {
    if (!inputs.name) throw new Error("name is required");
    let apiUrl: URL;
    try {
        apiUrl = new URL(inputs.apiUrl);
    } catch {
        throw new Error("apiUrl must be an absolute HTTP(S) URL");
    }
    if (
        !["https:", "http:"].includes(apiUrl.protocol) ||
        !apiUrl.hostname ||
        apiUrl.username ||
        apiUrl.password ||
        apiUrl.search ||
        apiUrl.hash ||
        (apiUrl.protocol === "http:" && !["localhost", "127.0.0.1", "[::1]"].includes(apiUrl.hostname))
    ) {
        throw new Error("apiUrl must use HTTPS (HTTP is allowed only for localhost)");
    }
    if (Boolean(inputs.image) === Boolean(inputs.repositoryUrl)) {
        throw new Error("set exactly one of image or repositoryUrl");
    }
    if (inputs.repositoryUrl && inputs.imagePullSecret) {
        throw new Error("imagePullSecret can only be used with an image");
    }
}

async function createDeployment(inputs: DeploymentInputs): Promise<DeploymentState> {
    const body: Record<string, string | number> = {
        name: inputs.name,
        port: inputs.port,
        cpu_millicores: inputs.cpuMillicores,
        memory_mb: inputs.memoryMb,
    };
    if (inputs.image) {
        body.image = inputs.image;
        if (inputs.imagePullSecret) body.image_pull_secret = inputs.imagePullSecret;
    } else {
        body.repository_url = inputs.repositoryUrl!;
        if (inputs.branch) body.branch = inputs.branch;
        body.dockerfile = inputs.dockerfile || "Dockerfile";
    }
    return request<DeploymentState>(inputs.apiUrl, inputs.token, "POST", "/api/v1/deployments", body);
}

async function waitForDeployment(inputs: DeploymentInputs, id: number): Promise<DeploymentState> {
    const deadline = Date.now() + inputs.waitTimeoutSeconds * 1000;
    while (Date.now() < deadline) {
        const deployment = await request<DeploymentState>(
            inputs.apiUrl,
            inputs.token,
            "GET",
            `/api/v1/deployments/${id}`,
        );
        if (deployment.status === "running") return deployment;
        if (["failed", "deleted", "cancelled"].includes(deployment.status)) {
            throw new Error(`Crelan deployment ${id} finished with status ${deployment.status}`);
        }
        await new Promise((resolve) => setTimeout(resolve, 3000));
    }
    throw new Error(`Timed out waiting for Crelan deployment ${id}`);
}

async function request<T>(
    rawApiUrl: string,
    token: string,
    method: string,
    path: string,
    body?: Record<string, string | number>,
): Promise<T> {
    const apiUrl = rawApiUrl.replace(/\/+$/, "");
    const response = await fetch(`${apiUrl}${path}`, {
        method,
        headers: {
            Authorization: `Bearer ${token}`,
            Accept: "application/json",
            ...(body ? { "Content-Type": "application/json" } : {}),
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
    });
    if (!response.ok) {
        const text = await response.text();
        let message = `Crelan API returned HTTP ${response.status}`;
        try {
            const error = JSON.parse(text) as { error?: string; code?: string };
            if (error.error) message = error.code ? `${error.error} (${error.code})` : error.error;
        } catch {
            if (text) message += `: ${text}`;
        }
        if (response.status === 404 && method === "DELETE") return undefined as T;
        throw new Error(message);
    }
    if (response.status === 204) return undefined as T;
    return (await response.json()) as T;
}

function outputsFor(inputs: DeploymentInputs, state: DeploymentState): DeploymentOutputs {
    return {
        ...inputs,
        deploymentId: String(state.id),
        status: state.status,
        url: state.url,
    };
}

export class Deployment extends pulumi.dynamic.Resource {
    public readonly deploymentId!: pulumi.Output<string>;
    public readonly status!: pulumi.Output<string>;
    public readonly url!: pulumi.Output<string | undefined>;

    constructor(name: string, args: DeploymentArgs, opts?: pulumi.CustomResourceOptions) {
        super(
            new CrelanDeploymentProvider(),
            name,
            {
                ...args,
                port: args.port ?? 8080,
                cpuMillicores: args.cpuMillicores ?? 1000,
                memoryMb: args.memoryMb ?? 512,
                waitForReady: args.waitForReady ?? true,
                waitTimeoutSeconds: args.waitTimeoutSeconds ?? 2700,
            },
            opts,
        );
    }
}

const config = new pulumi.Config("crelan");
const deployment = new Deployment("app", {
    apiUrl: config.require("apiUrl"),
    token: config.requireSecret("token"),
    name: config.require("appName"),
    image: config.get("image") || undefined,
    imagePullSecret: config.get("imagePullSecret") || undefined,
    repositoryUrl: config.get("repositoryUrl") || undefined,
    branch: config.get("branch") || undefined,
    port: config.getNumber("port") ?? 8080,
});

export const deploymentId = deployment.deploymentId;
export const deploymentStatus = deployment.status;
export const deploymentUrl = deployment.url;
