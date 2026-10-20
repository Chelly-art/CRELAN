import * as pulumi from "@pulumi/pulumi";
import { Deployment } from "./provider";

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

export { Deployment } from "./provider";
export type { DeploymentArgs } from "./provider";
export const deploymentId = deployment.deploymentId;
export const deploymentStatus = deployment.status;
export const deploymentUrl = deployment.url;
