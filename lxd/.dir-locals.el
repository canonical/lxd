;;; Directory Local Variables
;;; For more information see (info "(emacs) Directory Variables")
((go-mode
  . ((go-test-args . "-tags libsqlite3 -timeout 35s")
     (eval
      . (set
	 (make-local-variable 'flycheck-go-build-tags)
	 '("libsqlite3")))
     (eval
      . (let* ((locals-path
     		(let ((d (dir-locals-find-file ".")))
     		  (if (stringp d) (file-name-directory d) (car d))))
	       (go-wrapper (s-concat locals-path ".go-wrapper"))
	       (go-rename-wrapper (s-concat locals-path ".go-rename-wrapper")))
     	  (progn
	    (set (make-local-variable 'go-command) go-wrapper)
	    (set (make-local-variable 'flycheck-go-build-executable) go-wrapper)
	    (set (make-local-variable 'go-rename-command) go-rename-wrapper)))))))
