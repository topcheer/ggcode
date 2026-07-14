export namespace hooks {
	
	export class Hook {
	    match: string;
	    match_mode: string;
	    type: string;
	    command: string;
	    url: string;
	    method: string;
	    headers: Record<string, string>;
	    timeout: string;
	    secret: string;
	    inject_output: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Hook(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.match = source["match"];
	        this.match_mode = source["match_mode"];
	        this.type = source["type"];
	        this.command = source["command"];
	        this.url = source["url"];
	        this.method = source["method"];
	        this.headers = source["headers"];
	        this.timeout = source["timeout"];
	        this.secret = source["secret"];
	        this.inject_output = source["inject_output"];
	    }
	}
	export class HookConfig {
	    on_user_message: Hook[];
	    pre_tool_use: Hook[];
	    post_tool_use: Hook[];
	    on_agent_stop: Hook[];
	    on_stream_stop: Hook[];
	
	    static createFrom(source: any = {}) {
	        return new HookConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.on_user_message = this.convertValues(source["on_user_message"], Hook);
	        this.pre_tool_use = this.convertValues(source["pre_tool_use"], Hook);
	        this.post_tool_use = this.convertValues(source["post_tool_use"], Hook);
	        this.on_agent_stop = this.convertValues(source["on_agent_stop"], Hook);
	        this.on_stream_stop = this.convertValues(source["on_stream_stop"], Hook);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace lanchat {
	
	export class Attachment {
	    id: string;
	    name: string;
	    size: number;
	    mime_type: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new Attachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.mime_type = source["mime_type"];
	        this.url = source["url"];
	    }
	}
	export class Message {
	    id: string;
	    from_node_id: string;
	    from_role: string;
	    from_nick: string;
	    to_node_id: string;
	    to_role: string;
	    content: string;
	    attachments?: Attachment[];
	    timestamp: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.from_node_id = source["from_node_id"];
	        this.from_role = source["from_role"];
	        this.from_nick = source["from_nick"];
	        this.to_node_id = source["to_node_id"];
	        this.to_role = source["to_role"];
	        this.content = source["content"];
	        this.attachments = this.convertValues(source["attachments"], Attachment);
	        this.timestamp = source["timestamp"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Participant {
	    node_id: string;
	    human_nick: string;
	    agent_nick: string;
	    mode: string;
	    endpoint: string;
	    role: string;
	    team: string;
	    online: boolean;
	    last_seen: number;
	    workspace?: string;
	    project_name?: string;
	    languages?: string[];
	    frameworks?: string[];
	    has_git?: boolean;
	    has_tests?: boolean;
	    agent_busy?: boolean;
	    udp_capable?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Participant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.node_id = source["node_id"];
	        this.human_nick = source["human_nick"];
	        this.agent_nick = source["agent_nick"];
	        this.mode = source["mode"];
	        this.endpoint = source["endpoint"];
	        this.role = source["role"];
	        this.team = source["team"];
	        this.online = source["online"];
	        this.last_seen = source["last_seen"];
	        this.workspace = source["workspace"];
	        this.project_name = source["project_name"];
	        this.languages = source["languages"];
	        this.frameworks = source["frameworks"];
	        this.has_git = source["has_git"];
	        this.has_tests = source["has_tests"];
	        this.agent_busy = source["agent_busy"];
	        this.udp_capable = source["udp_capable"];
	    }
	}
	export class PendingAgentMsg {
	    message: Message;
	    // Go type: time
	    received: any;
	
	    static createFrom(source: any = {}) {
	        return new PendingAgentMsg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = this.convertValues(source["message"], Message);
	        this.received = this.convertValues(source["received"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class ClipboardAttachment {
	    path?: string;
	    name: string;
	    size: number;
	    mimeType?: string;
	    kind: string;
	    content?: string;
	    data?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ClipboardAttachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.mimeType = source["mimeType"];
	        this.kind = source["kind"];
	        this.content = source["content"];
	        this.data = source["data"];
	        this.error = source["error"];
	    }
	}
	export class FileBinaryData {
	    mimeType: string;
	    data: string;
	
	    static createFrom(source: any = {}) {
	        return new FileBinaryData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mimeType = source["mimeType"];
	        this.data = source["data"];
	    }
	}
	export class PastedImage {
	    mimeType: string;
	    data: string;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new PastedImage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mimeType = source["mimeType"];
	        this.data = source["data"];
	        this.name = source["name"];
	    }
	}
	export class ShareInfo {
	    connectURL: string;
	    qrCodeBase64: string;
	
	    static createFrom(source: any = {}) {
	        return new ShareInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connectURL = source["connectURL"];
	        this.qrCodeBase64 = source["qrCodeBase64"];
	    }
	}
	export class StreamEventEnvelope {
	    type: string;
	    data: string;
	
	    static createFrom(source: any = {}) {
	        return new StreamEventEnvelope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.data = source["data"];
	    }
	}

}

export namespace swarm {
	
	export class TeamBoardTask {
	    id: string;
	    subject: string;
	    description?: string;
	    activeForm?: string;
	    status: string;
	    owner?: string;
	    assignee?: string;
	    blocks?: string[];
	    blockedBy?: string[];
	    metadata?: Record<string, string>;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new TeamBoardTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.subject = source["subject"];
	        this.description = source["description"];
	        this.activeForm = source["activeForm"];
	        this.status = source["status"];
	        this.owner = source["owner"];
	        this.assignee = source["assignee"];
	        this.blocks = source["blocks"];
	        this.blockedBy = source["blockedBy"];
	        this.metadata = source["metadata"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TeamBoardTeammate {
	    id: string;
	    name: string;
	    color?: string;
	    status: string;
	    currentTask?: string;
	    lastResult?: string;
	
	    static createFrom(source: any = {}) {
	        return new TeamBoardTeammate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.color = source["color"];
	        this.status = source["status"];
	        this.currentTask = source["currentTask"];
	        this.lastResult = source["lastResult"];
	    }
	}
	export class TeamBoardSnapshot {
	    id: string;
	    name: string;
	    leaderID: string;
	    teammates: TeamBoardTeammate[];
	    tasks: TeamBoardTask[];
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new TeamBoardSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.leaderID = source["leaderID"];
	        this.teammates = this.convertValues(source["teammates"], TeamBoardTeammate);
	        this.tasks = this.convertValues(source["tasks"], TeamBoardTask);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace wailskit {
	
	export class EndpointDetails {
	    displayName: string;
	    protocol: string;
	    baseUrl: string;
	    apiKeySet: boolean;
	    apiKeyMasked: string;
	    defaultModel: string;
	    models: string[];
	    contextWindow: number;
	    maxTokens: number;
	    authType: string;
	    supportsVision: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EndpointDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.displayName = source["displayName"];
	        this.protocol = source["protocol"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKeySet = source["apiKeySet"];
	        this.apiKeyMasked = source["apiKeyMasked"];
	        this.defaultModel = source["defaultModel"];
	        this.models = source["models"];
	        this.contextWindow = source["contextWindow"];
	        this.maxTokens = source["maxTokens"];
	        this.authType = source["authType"];
	        this.supportsVision = source["supportsVision"];
	    }
	}
	export class EndpointInfo {
	    key: string;
	    displayName: string;
	
	    static createFrom(source: any = {}) {
	        return new EndpointInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.displayName = source["displayName"];
	    }
	}
	export class EndpointPresetInfo {
	    id: string;
	    displayName: string;
	    models: string[];
	    defaultEndpoint: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EndpointPresetInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.models = source["models"];
	        this.defaultEndpoint = source["defaultEndpoint"];
	    }
	}
	export class FullConfig {
	    vendor: string;
	    endpoint: string;
	    model: string;
	    apiKeySet: boolean;
	    language: string;
	    extraPrompt: string;
	    defaultMode: string;
	    maxIterations: number;
	    probeContext: boolean;
	    impersonatePreset: string;
	    impersonateCustomVersion: string;
	    impersonateCustomHeaders: Record<string, string>;
	    subAgentMaxConcurrent: number;
	    subAgentTimeout: string;
	    subAgentShowOutput: boolean;
	    swarmMaxTeammates: number;
	    swarmTimeout: string;
	    swarmInboxSize: number;
	    a2aDisabled: boolean;
	    a2aPort: number;
	    a2aHost: string;
	    harnessAutoRun: string;
	    harnessAutoInit: boolean;
	    streamEncoder: string;
	    streamFPS: number;
	    knightEnabled: boolean;
	    knightTrustLevel: string;
	    sidebarVisible?: boolean;
	    workDir: string;
	    needsSetup: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FullConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vendor = source["vendor"];
	        this.endpoint = source["endpoint"];
	        this.model = source["model"];
	        this.apiKeySet = source["apiKeySet"];
	        this.language = source["language"];
	        this.extraPrompt = source["extraPrompt"];
	        this.defaultMode = source["defaultMode"];
	        this.maxIterations = source["maxIterations"];
	        this.probeContext = source["probeContext"];
	        this.impersonatePreset = source["impersonatePreset"];
	        this.impersonateCustomVersion = source["impersonateCustomVersion"];
	        this.impersonateCustomHeaders = source["impersonateCustomHeaders"];
	        this.subAgentMaxConcurrent = source["subAgentMaxConcurrent"];
	        this.subAgentTimeout = source["subAgentTimeout"];
	        this.subAgentShowOutput = source["subAgentShowOutput"];
	        this.swarmMaxTeammates = source["swarmMaxTeammates"];
	        this.swarmTimeout = source["swarmTimeout"];
	        this.swarmInboxSize = source["swarmInboxSize"];
	        this.a2aDisabled = source["a2aDisabled"];
	        this.a2aPort = source["a2aPort"];
	        this.a2aHost = source["a2aHost"];
	        this.harnessAutoRun = source["harnessAutoRun"];
	        this.harnessAutoInit = source["harnessAutoInit"];
	        this.streamEncoder = source["streamEncoder"];
	        this.streamFPS = source["streamFPS"];
	        this.knightEnabled = source["knightEnabled"];
	        this.knightTrustLevel = source["knightTrustLevel"];
	        this.sidebarVisible = source["sidebarVisible"];
	        this.workDir = source["workDir"];
	        this.needsSetup = source["needsSetup"];
	    }
	}
	export class IMAdapterInfo {
	    name: string;
	    enabled: boolean;
	    muted: boolean;
	    platform: string;
	    transport: string;
	    command: string;
	    args?: string[];
	    extra?: Record<string, any>;
	    targets?: string[];
	    workspace?: string;
	    isCurrent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IMAdapterInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.muted = source["muted"];
	        this.platform = source["platform"];
	        this.transport = source["transport"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.extra = source["extra"];
	        this.targets = source["targets"];
	        this.workspace = source["workspace"];
	        this.isCurrent = source["isCurrent"];
	    }
	}
	export class IMPlatformField {
	    key: string;
	    label: string;
	    placeholder: string;
	    secret?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IMPlatformField(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.placeholder = source["placeholder"];
	        this.secret = source["secret"];
	    }
	}
	export class IMPlatformMeta {
	    id: string;
	    displayName: string;
	    fields: IMPlatformField[];
	    qrAuth: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IMPlatformMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.fields = this.convertValues(source["fields"], IMPlatformField);
	        this.qrAuth = source["qrAuth"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImpersonationPresetInfo {
	    id: string;
	    displayName: string;
	    defaultVersion: string;
	    extraHeaders?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new ImpersonationPresetInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.defaultVersion = source["defaultVersion"];
	        this.extraHeaders = source["extraHeaders"];
	    }
	}
	export class LSPInstallOption {
	    id: string;
	    label: string;
	    binary: string;
	    recommended: boolean;
	    scope: string;
	
	    static createFrom(source: any = {}) {
	        return new LSPInstallOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.binary = source["binary"];
	        this.recommended = source["recommended"];
	        this.scope = source["scope"];
	    }
	}
	export class LSPInstallResult {
	    success: boolean;
	    output: string;
	
	    static createFrom(source: any = {}) {
	        return new LSPInstallResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.output = source["output"];
	    }
	}
	export class LSPServerStatus {
	    id: string;
	    display_name: string;
	    available: boolean;
	    binary: string;
	    install_hint: string;
	    override: boolean;
	    can_install: boolean;
	    install_options: LSPInstallOption[];
	
	    static createFrom(source: any = {}) {
	        return new LSPServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.display_name = source["display_name"];
	        this.available = source["available"];
	        this.binary = source["binary"];
	        this.install_hint = source["install_hint"];
	        this.override = source["override"];
	        this.can_install = source["can_install"];
	        this.install_options = this.convertValues(source["install_options"], LSPInstallOption);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LSPStatusResponse {
	    workspace: string;
	    languages: LSPServerStatus[];
	
	    static createFrom(source: any = {}) {
	        return new LSPStatusResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = source["workspace"];
	        this.languages = this.convertValues(source["languages"], LSPServerStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MCPOAuthStartResult {
	    serverName: string;
	    authorizeUrl: string;
	    deviceUserCode?: string;
	    openError?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPOAuthStartResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverName = source["serverName"];
	        this.authorizeUrl = source["authorizeUrl"];
	        this.deviceUserCode = source["deviceUserCode"];
	        this.openError = source["openError"];
	    }
	}
	export class MCPServerInfo {
	    name: string;
	    type?: string;
	    command?: string;
	    args?: string[];
	    env?: Record<string, string>;
	    url?: string;
	    headers?: Record<string, string>;
	    status?: string;
	    error?: string;
	    disabled?: boolean;
	    connected?: boolean;
	    oauthRequired?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MCPServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.url = source["url"];
	        this.headers = source["headers"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.disabled = source["disabled"];
	        this.connected = source["connected"];
	        this.oauthRequired = source["oauthRequired"];
	    }
	}
	export class ModelLimitInfo {
	    model: string;
	    contextWindow: number;
	    maxTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelLimitInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.contextWindow = source["contextWindow"];
	        this.maxTokens = source["maxTokens"];
	    }
	}
	export class ResolvedEndpointInfo {
	    vendorId: string;
	    vendorName: string;
	    endpointId: string;
	    endpointName: string;
	    protocol: string;
	    baseUrl: string;
	    apiKeySet: boolean;
	    apiKeyMasked: string;
	    model: string;
	    models: string[];
	    contextWindow: number;
	    supportsVision: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResolvedEndpointInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vendorId = source["vendorId"];
	        this.vendorName = source["vendorName"];
	        this.endpointId = source["endpointId"];
	        this.endpointName = source["endpointName"];
	        this.protocol = source["protocol"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKeySet = source["apiKeySet"];
	        this.apiKeyMasked = source["apiKeyMasked"];
	        this.model = source["model"];
	        this.models = source["models"];
	        this.contextWindow = source["contextWindow"];
	        this.supportsVision = source["supportsVision"];
	    }
	}
	export class SessionInfo {
	    id: string;
	    title: string;
	    workspace: string;
	    vendor: string;
	    model: string;
	    msgCount: number;
	    updatedAt: string;
	    locked: boolean;
	    lastMessage: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.workspace = source["workspace"];
	        this.vendor = source["vendor"];
	        this.model = source["model"];
	        this.msgCount = source["msgCount"];
	        this.updatedAt = source["updatedAt"];
	        this.locked = source["locked"];
	        this.lastMessage = source["lastMessage"];
	    }
	}
	export class SessionLimitInfo {
	    contextWindow: number;
	    maxTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionLimitInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contextWindow = source["contextWindow"];
	        this.maxTokens = source["maxTokens"];
	    }
	}
	export class SessionMessage {
	    id?: string;
	    turn_id?: string;
	    role: string;
	    content: string;
	    toolName?: string;
	    toolID?: string;
	    toolArgs?: string;
	    toolDisplayName?: string;
	    toolDetail?: string;
	    isError?: boolean;
	    streaming?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SessionMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turn_id = source["turn_id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.toolName = source["toolName"];
	        this.toolID = source["toolID"];
	        this.toolArgs = source["toolArgs"];
	        this.toolDisplayName = source["toolDisplayName"];
	        this.toolDetail = source["toolDetail"];
	        this.isError = source["isError"];
	        this.streaming = source["streaming"];
	    }
	}
	export class TestHookMatchResult {
	    matched: boolean;
	    error: string;

	    static createFrom(source: any = {}) {
	        return new TestHookMatchResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.matched = source["matched"];
	        this.error = source["error"];
	    }
	}
	export class TestEndpointResult {
	    ok: boolean;
	    message: string;
	    models?: string[];
	    modelCount: number;
	
	    static createFrom(source: any = {}) {
	        return new TestEndpointResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.models = source["models"];
	        this.modelCount = source["modelCount"];
	    }
	}
	export class VendorPresetInfo {
	    id: string;
	    displayName: string;
	    endpoints: EndpointPresetInfo[];
	
	    static createFrom(source: any = {}) {
	        return new VendorPresetInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.endpoints = this.convertValues(source["endpoints"], EndpointPresetInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

