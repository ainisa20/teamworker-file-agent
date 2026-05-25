export namespace agent {
	
	export class State {
	    status: string;
	    message: string;
	    serverUrl: string;
	    code: string;
	    sharedDir: string;
	    tunnelInfo: string;
	
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
	    }
	}

}

