export namespace wailskit {
	
	export class ConfigSnapshot {
	    vendor: string;
	    endpoint: string;
	    model: string;
	    defaultMode: string;
	    language: string;
	    extraPrompt: string;
	    needsSetup: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfigSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vendor = source["vendor"];
	        this.endpoint = source["endpoint"];
	        this.model = source["model"];
	        this.defaultMode = source["defaultMode"];
	        this.language = source["language"];
	        this.extraPrompt = source["extraPrompt"];
	        this.needsSetup = source["needsSetup"];
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
	export class IMAdapterInfo {
	    name: string;
	    enabled: boolean;
	    platform: string;
	    transport: string;
	    command: string;
	    args?: string[];
	    extra?: Record<string, any>;
	
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

