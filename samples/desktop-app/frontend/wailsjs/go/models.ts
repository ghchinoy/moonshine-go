export namespace main {
	
	export class LineOutput {
	    id: number;
	    start_time: number;
	    duration: number;
	    text: string;
	    is_complete: boolean;
	    mean_confidence: number;
	
	    static createFrom(source: any = {}) {
	        return new LineOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.start_time = source["start_time"];
	        this.duration = source["duration"];
	        this.text = source["text"];
	        this.is_complete = source["is_complete"];
	        this.mean_confidence = source["mean_confidence"];
	    }
	}
	export class BatchOutput {
	    lines: LineOutput[];
	    audio_duration_sec: number;
	    inference_ms: number;
	    real_time_factor: number;
	
	    static createFrom(source: any = {}) {
	        return new BatchOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lines = this.convertValues(source["lines"], LineOutput);
	        this.audio_duration_sec = source["audio_duration_sec"];
	        this.inference_ms = source["inference_ms"];
	        this.real_time_factor = source["real_time_factor"];
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

