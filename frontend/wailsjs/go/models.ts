export namespace agent {
	
	export class State {
	    status: string;
	    message: string;
	    serverUrl: string;
	    code: string;
	    sharedDir: string;
	    tunnelInfo: string;
	    acpEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.message = source["message"];
	        this.serverUrl = source["serverUrl"];
	        this.code = source["code"];
	        this.sharedDir = source["sharedDir"];
	        this.tunnelInfo = source["tunnelInfo"];
	        this.acpEnabled = source["acpEnabled"];
	    }
	}

}

export namespace main {
	
	export class ACPAgent {
	    id: string;
	    name: string;
	    icon: string;
	    installType: string;
	    installCmd: string;
	    checkCmd: string;
	    installed: boolean;
	    version: string;
	    helpUrl: string;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ACPAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.icon = source["icon"];
	        this.installType = source["installType"];
	        this.installCmd = source["installCmd"];
	        this.checkCmd = source["checkCmd"];
	        this.installed = source["installed"];
	        this.version = source["version"];
	        this.helpUrl = source["helpUrl"];
	        this.selected = source["selected"];
	    }
	}
	export class EnvStatus {
	    hasNpm: boolean;
	    npmVer: string;
	    hasPip: boolean;
	    pipVer: string;
	
	    static createFrom(source: any = {}) {
	        return new EnvStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasNpm = source["hasNpm"];
	        this.npmVer = source["npmVer"];
	        this.hasPip = source["hasPip"];
	        this.pipVer = source["pipVer"];
	    }
	}

}

