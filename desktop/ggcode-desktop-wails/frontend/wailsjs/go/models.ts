export namespace wailskit {
	
	export class ConfigSnapshot {
	    vendor: string;
	    endpoint: string;
	    model: string;
	    defaultMode: string;
	    language: string;
	    extraPrompt: string;
	
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
	export class FileInfo {
	    name: string;
	    isDir: boolean;
	    size: number;
	    modified: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new FileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.modified = source["modified"];
	        this.path = source["path"];
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

}

