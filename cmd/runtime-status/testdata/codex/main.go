// Synthetic Codex process for system tests. Never invokes a model or reads auth.
package main
import("bufio";"encoding/json";"fmt";"os";"path/filepath";"time")
func main(){
 if len(os.Args)>1 && os.Args[1]=="--version"{fmt.Println("codex-cli 0.153.4");return}
 if len(os.Args)>1 && os.Args[1]=="app-server"{
  scan:=bufio.NewScanner(os.Stdin)
  for scan.Scan(){var in map[string]any;json.Unmarshal(scan.Bytes(),&in);if in["id"]==nil{continue};result:=map[string]any{};if in["method"]=="account/rateLimits/read"{result["rateLimits"]=map[string]any{"primary":map[string]any{"windowDurationMins":300,"usedPercent":25,"resetsAt":time.Now().Unix()+3600}}};json.NewEncoder(os.Stdout).Encode(map[string]any{"id":in["id"],"result":result})};return
 }
 cwd,_:=os.Getwd();id:="12345678-1234-4234-8234-123456789abc"
 dir:=filepath.Join(os.Getenv("CODEX_HOME"),"sessions");os.MkdirAll(dir,0700)
 f,err:=os.Create(filepath.Join(dir,"rollout-test-"+id+".jsonl"));if err!=nil{panic(err)};defer f.Close()
 out:=json.NewEncoder(f)
 row:=func(kind string,payload map[string]any){out.Encode(map[string]any{"type":kind,"timestamp":time.Now().UTC().Format(time.RFC3339Nano),"payload":payload})}
 row("session_meta",map[string]any{"id":id,"session_id":id,"source":"cli","cli_version":"0.153.4","cwd":cwd})
 row("turn_context",map[string]any{"model":"fixture-model","effort":"high"})
 row("event_msg",map[string]any{"type":"token_count","info":map[string]any{"model_context_window":200000,"last_token_usage":map[string]any{"total_tokens":50000,"input_tokens":40000,"cached_input_tokens":10000}}})
 for i:=0;i<100;i++{fmt.Println("Fixture history line",i)}
 fmt.Println("Fixture ready")
 scan:=bufio.NewScanner(os.Stdin);for scan.Scan(){if scan.Text()=="quit"{return}}
}
