package main
import (
 "fmt"
 "os"
 "os/exec"
 "strings"
 "sync"
 "testing"
)
func TestReviewMultiProcessConfigChild(t *testing.T) {
 if os.Getenv("REVIEW_CONFIG_CHILD")!="1" {return}
 for i:=0;i<30;i++ {
  if err:=saveConfig(Config{Paths:[]string{strings.Repeat(os.Getenv("REVIEW_CONFIG_TAG"),256*1024)}}); err!=nil { t.Errorf("save failed: %v",err) }
 }
}
func TestReviewMultiProcessConfig(t *testing.T) {
 exe,err:=os.Executable(); if err!=nil {t.Fatal(err)}
 var wg sync.WaitGroup
 outputs:=make(chan string,4)
 for i:=0;i<4;i++ {
  wg.Add(1)
  go func(i int) {defer wg.Done(); cmd:=exec.Command(exe,"-test.run=^TestReviewMultiProcessConfigChild$","-test.count=1"); cmd.Env=append(os.Environ(),"REVIEW_CONFIG_CHILD=1",fmt.Sprintf("REVIEW_CONFIG_TAG=%d",i)); out,err:=cmd.CombinedOutput(); if err!=nil {outputs<-string(out)} }(i)
 }
 wg.Wait(); close(outputs)
 failures:=0
 for out:=range outputs { failures++; t.Logf("child failure: %s",out) }
 _,err=loadConfig()
 t.Logf("failed child processes=%d/4; final config load error=%v",failures,err)
}