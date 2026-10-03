import v10Capabilities from "@unit-test-ide/protocol-schema/v1/capabilities" with { type: "json" };
import v10Message from "@unit-test-ide/protocol-schema/v1/message" with { type: "json" };
import v11Artifact from "@unit-test-ide/protocol-schema/v1.1/artifact" with { type: "json" };
import v11Capabilities from "@unit-test-ide/protocol-schema/v1.1/capabilities" with { type: "json" };
import v11Event from "@unit-test-ide/protocol-schema/v1.1/event" with { type: "json" };
import v11Message from "@unit-test-ide/protocol-schema/v1.1/message" with { type: "json" };
import v11Task from "@unit-test-ide/protocol-schema/v1.1/task" with { type: "json" };
import v12Artifact from "@unit-test-ide/protocol-schema/v1.2/artifact" with { type: "json" };
import v12Capabilities from "@unit-test-ide/protocol-schema/v1.2/capabilities" with { type: "json" };
import v12Diagnostic from "@unit-test-ide/protocol-schema/v1.2/diagnostic" with { type: "json" };
import v12Event from "@unit-test-ide/protocol-schema/v1.2/event" with { type: "json" };
import v12Message from "@unit-test-ide/protocol-schema/v1.2/message" with { type: "json" };
import v12Task from "@unit-test-ide/protocol-schema/v1.2/task" with { type: "json" };
import v12Workspace from "@unit-test-ide/protocol-schema/v1.2/workspace" with { type: "json" };
import v13Artifact from "@unit-test-ide/protocol-schema/v1.3/artifact" with { type: "json" };
import v13Capabilities from "@unit-test-ide/protocol-schema/v1.3/capabilities" with { type: "json" };
import v13Diagnostic from "@unit-test-ide/protocol-schema/v1.3/diagnostic" with { type: "json" };
import v13Event from "@unit-test-ide/protocol-schema/v1.3/event" with { type: "json" };
import v13Message from "@unit-test-ide/protocol-schema/v1.3/message" with { type: "json" };
import v13Task from "@unit-test-ide/protocol-schema/v1.3/task" with { type: "json" };
import v13Test from "@unit-test-ide/protocol-schema/v1.3/test" with { type: "json" };
import v14Artifact from "@unit-test-ide/protocol-schema/v1.4/artifact" with { type: "json" };
import v14Capabilities from "@unit-test-ide/protocol-schema/v1.4/capabilities" with { type: "json" };
import v14Coverage from "@unit-test-ide/protocol-schema/v1.4/coverage" with { type: "json" };
import v14Diagnostic from "@unit-test-ide/protocol-schema/v1.4/diagnostic" with { type: "json" };
import v14Event from "@unit-test-ide/protocol-schema/v1.4/event" with { type: "json" };
import v14Message from "@unit-test-ide/protocol-schema/v1.4/message" with { type: "json" };
import v14Task from "@unit-test-ide/protocol-schema/v1.4/task" with { type: "json" };
import v14Test from "@unit-test-ide/protocol-schema/v1.4/test" with { type: "json" };
import v15Artifact from "@unit-test-ide/protocol-schema/v1.5/artifact" with { type: "json" };
import v15Capabilities from "@unit-test-ide/protocol-schema/v1.5/capabilities" with { type: "json" };
import v15Coverage from "@unit-test-ide/protocol-schema/v1.5/coverage" with { type: "json" };
import v15Diagnostic from "@unit-test-ide/protocol-schema/v1.5/diagnostic" with { type: "json" };
import v15Event from "@unit-test-ide/protocol-schema/v1.5/event" with { type: "json" };
import v15Message from "@unit-test-ide/protocol-schema/v1.5/message" with { type: "json" };
import v15Task from "@unit-test-ide/protocol-schema/v1.5/task" with { type: "json" };
import v15TestGeneration from "@unit-test-ide/protocol-schema/v1.5/test-generation" with { type: "json" };
import v15Test from "@unit-test-ide/protocol-schema/v1.5/test" with { type: "json" };
import v16Artifact from "@unit-test-ide/protocol-schema/v1.6/artifact" with { type: "json" };
import v16Capabilities from "@unit-test-ide/protocol-schema/v1.6/capabilities" with { type: "json" };
import v16Coverage from "@unit-test-ide/protocol-schema/v1.6/coverage" with { type: "json" };
import v16Diagnostic from "@unit-test-ide/protocol-schema/v1.6/diagnostic" with { type: "json" };
import v16Event from "@unit-test-ide/protocol-schema/v1.6/event" with { type: "json" };
import v16Message from "@unit-test-ide/protocol-schema/v1.6/message" with { type: "json" };
import v16Task from "@unit-test-ide/protocol-schema/v1.6/task" with { type: "json" };
import v16TestGeneration from "@unit-test-ide/protocol-schema/v1.6/test-generation" with { type: "json" };
import v16Test from "@unit-test-ide/protocol-schema/v1.6/test" with { type: "json" };

const schemas = {
  "v1/capabilities": v10Capabilities,
  "v1/message": v10Message,
  "v1.1/artifact": v11Artifact,
  "v1.1/capabilities": v11Capabilities,
  "v1.1/event": v11Event,
  "v1.1/message": v11Message,
  "v1.1/task": v11Task,
  "v1.2/artifact": v12Artifact,
  "v1.2/capabilities": v12Capabilities,
  "v1.2/diagnostic": v12Diagnostic,
  "v1.2/event": v12Event,
  "v1.2/message": v12Message,
  "v1.2/task": v12Task,
  "v1.2/workspace": v12Workspace,
  "v1.3/artifact": v13Artifact,
  "v1.3/capabilities": v13Capabilities,
  "v1.3/diagnostic": v13Diagnostic,
  "v1.3/event": v13Event,
  "v1.3/message": v13Message,
  "v1.3/task": v13Task,
  "v1.3/test": v13Test,
  "v1.4/artifact": v14Artifact,
  "v1.4/capabilities": v14Capabilities,
  "v1.4/coverage": v14Coverage,
  "v1.4/diagnostic": v14Diagnostic,
  "v1.4/event": v14Event,
  "v1.4/message": v14Message,
  "v1.4/task": v14Task,
  "v1.4/test": v14Test,
  "v1.5/artifact": v15Artifact,
  "v1.5/capabilities": v15Capabilities,
  "v1.5/coverage": v15Coverage,
  "v1.5/diagnostic": v15Diagnostic,
  "v1.5/event": v15Event,
  "v1.5/message": v15Message,
  "v1.5/task": v15Task,
  "v1.5/test-generation": v15TestGeneration,
  "v1.5/test": v15Test,
  "v1.6/artifact": v16Artifact,
  "v1.6/capabilities": v16Capabilities,
  "v1.6/coverage": v16Coverage,
  "v1.6/diagnostic": v16Diagnostic,
  "v1.6/event": v16Event,
  "v1.6/message": v16Message,
  "v1.6/task": v16Task,
  "v1.6/test-generation": v16TestGeneration,
  "v1.6/test": v16Test,
} as const;

type ProtocolSchemaKey = keyof typeof schemas;
const protocolSchemaPrefix = "@unit-test-ide/protocol-schema/" as const;
export type ProtocolSchemaPath = `${typeof protocolSchemaPrefix}${ProtocolSchemaKey}`;

export function protocolSchema(path: ProtocolSchemaPath): object {
  const key = path.slice(protocolSchemaPrefix.length) as ProtocolSchemaKey;
  const schema = schemas[key];
  if (!schema) throw new Error(`unknown protocol schema: ${path}`);
  return schema;
}
