package main

import (
 "archive/zip"
 "os"
 "path/filepath"
 "testing"
)

func TestReviewReproductions(t *testing.T) {
 for _, tc := range []struct{name string; files map[string]string; dataLost, missingExe bool}{
  {"existing_data_in_zip", map[string]string{"Code.exe":"new", "resources/app/package.json":`{"version":"1.100.0"}`, "data/default.txt":"default"}, true, false},
  {"single_directory_without_executable", map[string]string{"bundle/resources/app/package.json":`{"version":"1.100.0"}`}, false, true},
 } {
  t.Run(tc.name, func(t *testing.T) {
   base:=t.TempDir()
   root:=makeTestInstall(t,filepath.Join(base,"install"),"1.99.0")
   zipPath:=filepath.Join(base,"update.zip")
   f,err:=os.Create(zipPath); if err!=nil {t.Fatal(err)}
   w:=zip.NewWriter(f)
   for name,body:=range tc.files {p,e:=w.Create(name); if e!=nil {t.Fatal(e)}; if _,e=p.Write([]byte(body)); e!=nil {t.Fatal(e)} }
   if err=w.Close(); err!=nil {t.Fatal(err)}; if err=f.Close(); err!=nil {t.Fatal(err)}
   version,err:=readVersionFromZip(zipPath); if err!=nil || version!="1.100.0" {t.Fatalf("version validation: %s %v",version,err)}
   info,err:=inspectInstall(root); if err!=nil {t.Fatal(err)}
   u:=NewUpdater(filepath.Join(base,"cache"),func(string){},nil)
   if err=u.replaceInstall(info,LatestPackage{Version:version,ZipPath:zipPath}); err!=nil {t.Fatal(err)}
   _,dataErr:=os.Stat(filepath.Join(root,"data","settings.txt"))
   _,exeErr:=os.Stat(filepath.Join(root,"Code.exe"))
   if os.IsNotExist(dataErr)!=tc.dataLost || os.IsNotExist(exeErr)!=tc.missingExe {t.Fatalf("unexpected reproduction: data=%v exe=%v",dataErr,exeErr)}
   backups,err:=filepath.Glob(root+".backup_old-*"); if err!=nil {t.Fatal(err)}
   if len(backups)!=0 {t.Fatalf("backups unexpectedly retained: %v",backups)}
   t.Logf("update returned success; dataLost=%v missingCodeExe=%v oldBackups=%d",tc.dataLost,tc.missingExe,len(backups))
  })
 }
}