export namespace wailskit {
	
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
	    a2aApiKey: string;
	    a2aLanDiscovery: boolean;
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
	        this.a2aApiKey = source["a2aApiKey"];
	        this.a2aLanDiscovery = source["a2aLanDiscovery"];
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
	export class MCPServerInfo {
	    name: string;
	    type?: string;
	    command?: string;
	    args?: string[];
	    env?: Record<string, string>;
	    url?: string;
	    headers?: Record<string, string>;
	
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
	    }
	}
	export class SessionMessage {
	    role: string;
	    content: string;
	    toolName?: string;
	    toolID?: string;
	    toolArgs?: string;
	    toolDisplayName?: string;
	    toolDetail?: string;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SessionMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.toolName = source["toolName"];
	        this.toolID = source["toolID"];
	        this.toolArgs = source["toolArgs"];
	        this.toolDisplayName = source["toolDisplayName"];
	        this.toolDetail = source["toolDetail"];
	        this.isError = source["isError"];
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

