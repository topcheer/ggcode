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

}

