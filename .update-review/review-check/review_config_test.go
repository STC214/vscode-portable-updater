package main
import (
 "encoding/json"
 "os"
 "strings"
 "sync"
 "testing"
)
func TestReviewConcurrentConfigSave(t *testing.T) {
 start:=make(chan struct{})
 var wg sync.WaitGroup
 var mu sync.Mutex
 failures:=0
 for i:=0;i<24;i++ {
  wg.Add(1)
  go func(i int) {defer wg.Done(); <-start
   cfg:=Config{Paths:[]string{strings.Repeat(string(rune('A'+i)),128*1024)}}
   if err:=saveConfig(cfg); err!=nil {mu.Lock(); failures++; mu.Unlock()}
  }(i)
 }
 close(start); wg.Wait()
 path,err:=configPath(); if err!=nil {t.Fatal(err)}
 data,readErr:=os.ReadFile(path)
 valid:=readErr==nil && json.Valid(data)
 t.Logf("concurrent saves=24 failed=%d finalJSONValid=%v readError=%v",failures,valid,readErr)
 if failures==0 && valid { t.Skip("No filesystem collision observed in this run; static overlap still exists") }
}